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
 * Checks the relay for a newer build of this app, downloads and verifies it, and hands it to Android's
 * installer. Both clients use it; the watch is the same app with a different [app] and version code.
 *
 * Android will not install silently: [install] opens the system confirmation (after the one-time
 * "allow this source" setting), so this class never claims an install succeeded. A missing release
 * key on the published APK is reported by the system installer, not here.
 */
class UpdateManager(
    context: Context,
    private val app: String,
    private val currentVersionCode: Long
) {
    private val context: Context = context.applicationContext
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
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
            val result = runCatching {
                RelayRepository.init(context)
                RelayRepository.updateInfo(app).getOrThrow()
            }
            result.fold(
                onSuccess = { info ->
                    if (info.version_code > currentVersionCode) {
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
            val destination = File(File(context.cacheDir, "updates"), "$app-${info.version_code}.apk")
            val result = runCatching {
                RelayRepository.downloadUpdate(app, info, destination) { read, total ->
                    val progress = if (total > 0) read.toFloat() / total else 0f
                    _state.update {
                        if (it.phase == UpdatePhase.DOWNLOADING) {
                            it.copy(progress = progress.coerceIn(0f, 1f))
                        } else {
                            it
                        }
                    }
                }.getOrThrow()
            }
            result.fold(
                onSuccess = { file ->
                    _state.update {
                        it.copy(
                            phase = if (canInstall()) UpdatePhase.READY else UpdatePhase.NEEDS_PERMISSION,
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
    fun install() {
        val current = _state.value
        val file = current.file ?: return
        if (!canInstall()) {
            _state.update { it.copy(phase = UpdatePhase.NEEDS_PERMISSION) }
            openInstallSettings()
            return
        }
        val uri = try {
            FileProvider.getUriForFile(context, "${context.packageName}.updates", file)
        } catch (e: IllegalArgumentException) {
            fail("install_unavailable")
            return
        }
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            context.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            fail("install_unavailable")
        }
    }

    /** After returning from the "install unknown apps" setting, show Install instead of Settings. */
    fun refreshInstallPermission() {
        if (_state.value.phase == UpdatePhase.NEEDS_PERMISSION && canInstall()) {
            _state.update { it.copy(phase = UpdatePhase.READY) }
        }
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

    private fun canInstall(): Boolean = context.packageManager.canRequestPackageInstalls()

    private fun openInstallSettings() {
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
                return
            } catch (e: ActivityNotFoundException) {
                // Try the next one.
            }
        }
        fail("install_settings_unavailable")
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
