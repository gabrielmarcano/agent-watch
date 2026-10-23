package com.gabriel.agentwatch.update

import com.gabriel.agentwatch.model.UpdateInfo
import com.gabriel.agentwatch.network.RelayError
import com.gabriel.agentwatch.network.awaitValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.io.IOException

class UpdateControllerTest {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val tempDir = File(System.getProperty("java.io.tmpdir"), "aw-update-test-${System.nanoTime()}")
    private lateinit var port: FakeUpdatePlatformPort
    private lateinit var controller: UpdateController

    @Before
    fun setUp() {
        tempDir.mkdirs()
        port = FakeUpdatePlatformPort(updatesDirectory = tempDir)
        controller = UpdateController(port = port, app = "phone", scope = scope)
    }

    @After
    fun tearDown() {
        controller.close()
        scope.cancel()
        tempDir.deleteRecursively()
    }

    @Test
    fun initialPhaseIsIdle() {
        assertEquals(UpdatePhase.IDLE, controller.state.value.phase)
        assertNull(controller.state.value.info)
        assertNull(controller.state.value.file)
    }

    @Test
    fun autoCheckDiscoversAvailableUpdate() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 12, version_name = "1.2.0"))

        controller.check(manual = false)

        val state = awaitValue(controller.state, what = "AVAILABLE update") { it.phase == UpdatePhase.AVAILABLE }
        assertEquals(UpdatePhase.AVAILABLE, state.phase)
        assertEquals(12L, state.info?.version_code)
        assertFalse(state.manual)
    }

    @Test
    fun autoCheckWhenUpToDateStaysIdle() {
        port.installedCode = 12
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 12, version_name = "1.2.0"))

        controller.check(manual = false)

        val state = awaitValue(controller.state, what = "check finished") { it.phase != UpdatePhase.CHECKING && it.info != null }
        assertEquals(UpdatePhase.IDLE, state.phase)
    }

    @Test
    fun manualCheckWhenUpToDateMovesToUpToDate() {
        port.installedCode = 12
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 12, version_name = "1.2.0"))

        controller.check(manual = true)

        val state = awaitValue(controller.state, what = "UP_TO_DATE phase") { it.phase == UpdatePhase.UP_TO_DATE }
        assertEquals(UpdatePhase.UP_TO_DATE, state.phase)
        assertTrue(state.manual)
    }

    @Test
    fun manualCheckWhenRelayHasNoBuildReportsUpToDate() {
        port.infoResult = Result.failure(RelayError("update_not_available", "no builds published", 404))

        controller.check(manual = true)

        val state = awaitValue(controller.state, what = "UP_TO_DATE for no build") { it.phase == UpdatePhase.UP_TO_DATE }
        assertEquals(UpdatePhase.UP_TO_DATE, state.phase)
    }

    @Test
    fun manualCheckWhenNetworkFailsMovesToFailed() {
        port.infoResult = Result.failure(IOException("connection refused"))

        controller.check(manual = true)

        val state = awaitValue(controller.state, what = "FAILED phase") { it.phase == UpdatePhase.FAILED }
        assertEquals(UpdatePhase.FAILED, state.phase)
        assertEquals("network", state.errorCode)
    }

    @Test
    fun autoCheckWhenNetworkFailsSilentlyResetsToIdle() {
        port.infoResult = Result.failure(IOException("offline"))

        controller.check(manual = false)

        val state = awaitValue(controller.state, what = "silent IDLE reset") { it.phase == UpdatePhase.IDLE }
        assertEquals(UpdatePhase.IDLE, state.phase)
        assertNull(state.errorCode)
    }

    @Test
    fun downloadTransitionsToReadyWhenPackageInstallsPermitted() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.canInstall = true

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }

        controller.download()

        val state = awaitValue(controller.state, what = "READY phase") { it.phase == UpdatePhase.READY }
        assertEquals(UpdatePhase.READY, state.phase)
        assertEquals(1f, state.progress)
        assertNotNull(state.file)
        assertTrue(state.file!!.name.contains("15"))
    }

    @Test
    fun downloadTransitionsToNeedsPermissionWhenPackageInstallsNotPermitted() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.canInstall = false

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }

        controller.download()

        val state = awaitValue(controller.state, what = "NEEDS_PERMISSION phase") { it.phase == UpdatePhase.NEEDS_PERMISSION }
        assertEquals(UpdatePhase.NEEDS_PERMISSION, state.phase)
        assertNotNull(state.file)
    }

    @Test
    fun downloadFailureWithBadChecksumMovesToFailed() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.downloadResult = { _, _, _ -> Result.failure(RelayError("bad_download", "sha256 mismatch", 0)) }

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }

        controller.download()

        val state = awaitValue(controller.state, what = "FAILED phase") { it.phase == UpdatePhase.FAILED }
        assertEquals(UpdatePhase.FAILED, state.phase)
        assertEquals("bad_download", state.errorCode)
        assertNull(state.file)
    }

    @Test
    fun cancelDownloadRestoresAvailablePhase() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.downloadResult = { _, _, _ ->
            // Simulates long running download that never returns immediately
            Thread.sleep(1000)
            Result.success(File(tempDir, "temp.apk"))
        }

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }

        controller.download()
        awaitValue(controller.state, what = "DOWNLOADING") { it.phase == UpdatePhase.DOWNLOADING }

        controller.cancelDownload()
        assertEquals(UpdatePhase.AVAILABLE, controller.state.value.phase)
    }

    @Test
    fun installLaunchesInstallerWhenPermitted() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.canInstall = true
        port.launchInstallerResult = true

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }
        controller.download()
        val ready = awaitValue(controller.state, what = "READY") { it.phase == UpdatePhase.READY }

        val launched = controller.install()
        assertTrue(launched)
        assertEquals(ready.file, port.launchedFile)
    }

    @Test
    fun installFailsWhenInstallerUnavailable() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.canInstall = true
        port.launchInstallerResult = false

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }
        controller.download()
        awaitValue(controller.state, what = "READY") { it.phase == UpdatePhase.READY }

        val launched = controller.install()
        assertFalse(launched)
        assertEquals(UpdatePhase.FAILED, controller.state.value.phase)
        assertEquals("install_unavailable", controller.state.value.errorCode)
    }

    @Test
    fun resumeInstallIfPermittedTransitionsFromNeedsPermissionToReadyAndCanLaunch() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))
        port.canInstall = false

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }
        controller.download()
        awaitValue(controller.state, what = "NEEDS_PERMISSION") { it.phase == UpdatePhase.NEEDS_PERMISSION }

        // User returned from settings without granting permission
        assertFalse(controller.resumeInstallIfPermitted(launch = false))
        assertEquals(UpdatePhase.NEEDS_PERMISSION, controller.state.value.phase)

        // User granted permission
        port.canInstall = true
        val resumed = controller.resumeInstallIfPermitted(launch = true)
        assertTrue(resumed)
        assertEquals(UpdatePhase.READY, controller.state.value.phase)
        assertNotNull(port.launchedFile)
    }

    @Test
    fun dismissResetsControllerToIdle() {
        port.installedCode = 10
        port.infoResult = Result.success(UpdateInfo(app = "phone", version_code = 15, size = 100))

        controller.check(manual = true)
        awaitValue(controller.state, what = "AVAILABLE") { it.phase == UpdatePhase.AVAILABLE }

        controller.dismiss()
        assertEquals(UpdatePhase.IDLE, controller.state.value.phase)
        assertNull(controller.state.value.info)
    }
}

private class FakeUpdatePlatformPort(
    var installedCode: Long = 10,
    var installedName: String = "1.0.0",
    var infoResult: Result<UpdateInfo> = Result.success(
        UpdateInfo(app = "phone", version_code = 11, version_name = "1.1.0", sha256 = "abc", size = 1024)
    ),
    var downloadResult: (UpdateInfo, File, (Long, Long) -> Unit) -> Result<File> = { _, dest, progress ->
        progress(512, 1024)
        progress(1024, 1024)
        Result.success(dest)
    },
    var canInstall: Boolean = true,
    var launchInstallerResult: Boolean = true,
    var openSettingsResult: Boolean = true,
    var updatesDirectory: File = File("/tmp")
) : UpdatePlatformPort {
    var launchedFile: File? = null
    var openedSettings: Boolean = false

    override fun installedVersionCode(): Long = installedCode
    override fun installedVersionName(): String = installedName
    override suspend fun fetchUpdateInfo(channel: String): Result<UpdateInfo> = infoResult
    override suspend fun downloadApk(
        info: UpdateInfo,
        destFile: File,
        onProgress: (read: Long, total: Long) -> Unit
    ): Result<File> = downloadResult(info, destFile, onProgress)

    override fun canInstallPackages(): Boolean = canInstall

    override fun launchInstaller(apkFile: File): Boolean {
        launchedFile = apkFile
        return launchInstallerResult
    }

    override fun openInstallSettings(): Boolean {
        openedSettings = true
        return openSettingsResult
    }

    override fun updatesDir(): File = updatesDirectory
}
