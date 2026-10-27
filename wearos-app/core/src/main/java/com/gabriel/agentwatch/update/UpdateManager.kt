package com.gabriel.agentwatch.update

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.core.content.FileProvider
import com.gabriel.agentwatch.model.UpdateInfo
import com.gabriel.agentwatch.network.RelayError
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.File
import java.io.IOException
import java.net.SocketTimeoutException

enum class UpdatePhase {
    IDLE,
    CHECKING,
    UP_TO_DATE,
    AVAILABLE,
    DOWNLOADING,
    READY,
    NEEDS_PERMISSION,
    FAILED
}

/**
 * What the update card shows. [errorCode] is a relay error code (`bad_download`, `not_paired`, …) or
 * one of `network`, `timeout`, `install_unavailable`, `install_settings_unavailable`; the UI maps it
 * to a string. [file] exists from [UpdatePhase.READY] on.
 */
data class UpdateState(
    val phase: UpdatePhase = UpdatePhase.IDLE,
    val info: UpdateInfo? = null,
    val progress: Float = 0f,
    val errorCode: String? = null,
    val file: File? = null,
    val manual: Boolean = false
)

/**
 * Seam for platform capabilities required by the update state machine:
 * version queries, downloading, permission checking, installer launching, and file storage.
 */
interface UpdatePlatformPort {
    fun installedVersionCode(): Long
    fun installedVersionName(): String = ""
    suspend fun fetchUpdateInfo(channel: String): Result<UpdateInfo>
    suspend fun downloadApk(
        info: UpdateInfo,
        destFile: File,
        onProgress: (read: Long, total: Long) -> Unit
    ): Result<File>
    fun canInstallPackages(): Boolean
    fun launchInstaller(apkFile: File): Boolean
    fun openInstallSettings(): Boolean
    fun updatesDir(): File
}

/**
 * Android implementation of [UpdatePlatformPort] backed by [Context], [FileProvider], and [RelayRepository].
 */
class AndroidUpdatePlatformPort(
    private val context: Context,
    private val app: String,
    private val currentVersionCode: Long,
    private val currentVersionName: String = ""
) : UpdatePlatformPort {
    override fun installedVersionCode(): Long = currentVersionCode
    override fun installedVersionName(): String = currentVersionName

    override suspend fun fetchUpdateInfo(channel: String): Result<UpdateInfo> = runCatching {
        RelayRepository.init(context)
        RelayRepository.updateInfo(channel).getOrThrow()
    }

    override suspend fun downloadApk(
        info: UpdateInfo,
        destFile: File,
        onProgress: (read: Long, total: Long) -> Unit
    ): Result<File> = RelayRepository.downloadUpdate(app, info, destFile, onProgress)

    override fun canInstallPackages(): Boolean = context.packageManager.canRequestPackageInstalls()

    override fun launchInstaller(apkFile: File): Boolean {
        val uri = try {
            FileProvider.getUriForFile(context, "${context.packageName}.updates", apkFile)
        } catch (e: IllegalArgumentException) {
            return false
        }
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            context.startActivity(intent)
            true
        } catch (e: ActivityNotFoundException) {
            false
        }
    }

    override fun openInstallSettings(): Boolean {
        val packageUri = Uri.parse("package:${context.packageName}")
        val intents = listOf(
            Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, packageUri),
            // Some Wear OS builds have no per-app "unknown sources" screen; the app's own
            // details page is the closest place the setting may live.
            Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, packageUri)
        )
        for (intent in intents) {
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            try {
                context.startActivity(intent)
                return true
            } catch (e: ActivityNotFoundException) {
                // Try the next one.
            }
        }
        return false
    }

    override fun updatesDir(): File = File(context.cacheDir, "updates")
}

/**
 * Pure 8-state machine controlling check, download, verification, permission handling, and install.
 */
class UpdateController(
    private val port: UpdatePlatformPort,
    private val app: String = "phone",
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
) {
    private var job: Job? = null

    private val _state = MutableStateFlow(UpdateState())
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    /**
     * Checks `GET /v1/updates/{app}`. Auto checks (on app start) fail silently and never replace an
     * update that is already offered; a manual check (Settings) reports "up to date" and failures.
     */
    fun check(manual: Boolean = false) {
        if (job?.isActive == true) return
        val previous = _state.value
        if (!manual && previous.phase in BUSY_OR_OFFERED) return

        job = scope.launch {
            _state.value = UpdateState(phase = UpdatePhase.CHECKING, info = previous.info, manual = manual)
            val result = port.fetchUpdateInfo(app)
            result.fold(
                onSuccess = { info ->
                    if (info.version_code > port.installedVersionCode()) {
                        _state.value = UpdateState(
                            phase = UpdatePhase.AVAILABLE,
                            info = info,
                            manual = manual
                        )
                    } else {
                        _state.value = UpdateState(
                            phase = if (manual) UpdatePhase.UP_TO_DATE else UpdatePhase.IDLE,
                            info = info,
                            manual = manual
                        )
                    }
                },
                onFailure = { error ->
                    val noBuild = error is RelayError && error.code == "update_not_available"
                    if (noBuild) {
                        _state.value = UpdateState(
                            phase = if (manual) UpdatePhase.UP_TO_DATE else UpdatePhase.IDLE,
                            info = previous.info,
                            manual = manual
                        )
                    } else if (manual) {
                        _state.value = UpdateState(
                            phase = UpdatePhase.FAILED,
                            info = previous.info,
                            errorCode = errorCode(error),
                            manual = true
                        )
                    } else {
                        // Offline at start is normal: no banner.
                        _state.value = UpdateState()
                    }
                }
            )
        }
    }

    /** Downloads and verifies the offered build. A retry starts from the beginning. */
    fun download() {
        val current = _state.value
        val info = current.info ?: return
        if (job?.isActive == true || current.phase !in setOf(UpdatePhase.AVAILABLE, UpdatePhase.FAILED)) return

        _state.value = current.copy(
            phase = UpdatePhase.DOWNLOADING,
            progress = 0f,
            errorCode = null,
            file = null
        )
        job = scope.launch {
            val destination = File(port.updatesDir(), "$app-${info.version_code}.apk")
            val result = port.downloadApk(info, destination) { read, total ->
                val progress = if (total > 0) read.toFloat() / total else 0f
                _state.update {
                    if (it.phase == UpdatePhase.DOWNLOADING) {
                        it.copy(progress = progress.coerceIn(0f, 1f))
                    } else {
                        it
                    }
                }
            }
            result.fold(
                onSuccess = { file ->
                    _state.update {
                        it.copy(
                            phase = if (port.canInstallPackages()) UpdatePhase.READY else UpdatePhase.NEEDS_PERMISSION,
                            progress = 1f,
                            file = file,
                            errorCode = null
                        )
                    }
                },
                onFailure = { error ->
                    if (error is CancellationException) return@fold
                    _state.update {
                        it.copy(phase = UpdatePhase.FAILED, errorCode = errorCode(error), file = null)
                    }
                }
            )
        }
    }

    /** Stops a download and offers it again. The partial file is removed by [RelayRepository]. */
    fun cancelDownload() {
        val info = _state.value.info ?: return
        job?.cancel()
        job = null
        _state.value = UpdateState(phase = UpdatePhase.AVAILABLE, info = info, manual = _state.value.manual)
    }

    /**
     * Opens the system installer for the downloaded APK, or the "install unknown apps" setting the
     * first time. Call again from Install after returning from Settings.
     */
    fun install(): Boolean {
        val current = _state.value
        val file = current.file ?: return false
        if (!port.canInstallPackages()) {
            _state.update { it.copy(phase = UpdatePhase.NEEDS_PERMISSION) }
            if (!port.openInstallSettings()) {
                fail("install_settings_unavailable")
            }
            return false
        }
        val launched = port.launchInstaller(file)
        if (!launched) {
            fail("install_unavailable")
            return false
        }
        return true
    }

    /** After returning from the "install unknown apps" setting, show Install instead of Settings. */
    fun resumeInstallIfPermitted(launch: Boolean = false): Boolean {
        if (_state.value.phase == UpdatePhase.NEEDS_PERMISSION && port.canInstallPackages()) {
            _state.update { it.copy(phase = UpdatePhase.READY) }
            if (launch) {
                return install()
            }
            return true
        }
        return false
    }

    /** After returning from the "install unknown apps" setting, show Install instead of Settings. */
    fun refreshInstallPermission() {
        resumeInstallIfPermitted(launch = false)
    }

    /** Hides the card; an in-flight download is cancelled rather than left running invisibly. */
    fun dismiss() {
        if (_state.value.phase == UpdatePhase.DOWNLOADING) {
            cancelDownload()
        }
        _state.value = UpdateState()
    }

    /** Frees the manager's coroutine. The cached APK stays in the app's cache. */
    fun close() {
        scope.cancel()
    }

    private fun fail(code: String) {
        _state.update { it.copy(phase = UpdatePhase.FAILED, errorCode = code) }
    }

    private fun errorCode(error: Throwable): String = when (error) {
        is RelayError -> error.code
        is SocketTimeoutException -> "timeout"
        is IOException -> "network"
        else -> "unknown"
    }

    private companion object {
        val BUSY_OR_OFFERED = setOf(
            UpdatePhase.AVAILABLE,
            UpdatePhase.DOWNLOADING,
            UpdatePhase.READY,
            UpdatePhase.NEEDS_PERMISSION
        )
    }
}

/**
 * Checks the relay for a newer build of this app, downloads and verifies it, and hands it to Android's
 * installer. Both clients use it; the watch is the same app with a different [app] and version code.
 *
 * Android will not install silently: [install] opens the system confirmation (after the one-time
 * "allow this source" setting), so this class never claims an install succeeded. A missing release
 * key on the published APK is reported by the system installer, not here.
 */
class UpdateManager(
    context: Context,
    app: String,
    currentVersionCode: Long,
    scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
    private val controller: UpdateController = UpdateController(
        port = AndroidUpdatePlatformPort(context.applicationContext, app, currentVersionCode),
        app = app,
        scope = scope
    )
) {
    val state: StateFlow<UpdateState> = controller.state

    /**
     * Checks `GET /v1/updates/{app}`. Auto checks (on app start) fail silently and never replace an
     * update that is already offered; a manual check (Settings) reports "up to date" and failures.
     */
    fun check(manual: Boolean = false) = controller.check(manual)

    /** Downloads and verifies the offered build. A retry starts from the beginning. */
    fun download() = controller.download()

    /** Stops a download and offers it again. The partial file is removed by [RelayRepository]. */
    fun cancelDownload() = controller.cancelDownload()

    /**
     * Opens the system installer for the downloaded APK, or the "install unknown apps" setting the
     * first time. Call again from Install after returning from Settings.
     */
    fun install(): Boolean = controller.install()

    /** After returning from the "install unknown apps" setting, optionally launches installer. */
    fun resumeInstallIfPermitted(launch: Boolean = false): Boolean = controller.resumeInstallIfPermitted(launch)

    /** After returning from the "install unknown apps" setting, show Install instead of Settings. */
    fun refreshInstallPermission() = controller.refreshInstallPermission()

    /** Hides the card; an in-flight download is cancelled rather than left running invisibly. */
    fun dismiss() = controller.dismiss()

    /** Frees the manager's coroutine. The cached APK stays in the app's cache. */
    fun close() = controller.close()
}
