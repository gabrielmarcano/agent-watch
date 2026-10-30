package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.approval.RelayErrorKind
import com.gabriel.agentwatch.approval.classifyRelayError
import com.gabriel.agentwatch.model.FolderListResponse
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.SessionCreated
import com.gabriel.agentwatch.model.SessionOptionsResponse
import com.gabriel.agentwatch.network.RelayError
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** The hosts that can start a session right now: the bridge is connected and its herdr is up. */
fun sessionHosts(hosts: List<HostInfo>): List<HostInfo> = hosts.filter { it.online && it.herdr_online }

/** A host's selectable agent kinds: trimmed, blank-free and unique, in the bridge's order. */
fun sessionKinds(options: SessionOptionsResponse?): List<String> =
    options?.kinds.orEmpty().map { it.trim() }.filter { it.isNotEmpty() }.distinct()

/**
 * The name proposed for a session in [path]: the folder's own name, so "…/agent-watch" starts an
 * "agent-watch" session. Falls back to "session" for a root, and for hosts whose path separator the
 * phone does not share (the bridge uses the last label either way).
 */
fun defaultSessionLabel(path: String?): String {
    val trimmed = path.orEmpty().trim().trimEnd('/')
    val name = trimmed.substringAfterLast('/').trim()
    return name.ifBlank { "session" }
}

/** Whether the form has everything the bridge needs, and is not already sending it. */
fun canCreateSession(host: String?, kind: String?, cwd: String?, creating: Boolean): Boolean =
    !creating && !host.isNullOrBlank() && !kind.isNullOrBlank() && !cwd.isNullOrBlank()

/**
 * Pluggable transport seam for fetching host options, browsing folders, and creating sessions.
 */
interface SessionTransport {
    suspend fun loadSessionOptions(hostId: String): Result<SessionOptionsResponse>
    suspend fun listFolders(hostId: String, path: String?): Result<FolderListResponse>
    suspend fun createSession(hostId: String, kind: String, cwd: String, title: String?): Result<SessionCreated>
}

/** Default transport backed by [RelayRepository]. */
object RelaySessionTransport : SessionTransport {
    override suspend fun loadSessionOptions(hostId: String): Result<SessionOptionsResponse> =
        RelayRepository.loadSessionOptions(hostId)

    override suspend fun listFolders(hostId: String, path: String?): Result<FolderListResponse> =
        RelayRepository.folders(hostId, path)

    override suspend fun createSession(
        hostId: String,
        kind: String,
        cwd: String,
        title: String?
    ): Result<SessionCreated> =
        RelayRepository.createSession(hostId, kind, cwd, title)
}

/**
 * Reactive state of the new session creation workflow.
 */
data class NewSessionUiState(
    val step: Int = 0,
    val selectedHostId: String = "",
    val options: SessionOptionsResponse? = null,
    val optionsError: RelayErrorKind? = null,
    val selectedKind: String = "",
    val viewPath: String = "",
    val folderListing: FolderListResponse? = null,
    val loadingFolders: Boolean = false,
    val foldersError: RelayErrorKind? = null,
    val chosenPath: String = "",
    val label: String = "",
    val creating: Boolean = false,
    val createError: RelayErrorKind? = null
)

/**
 * Controller encapsulating the 3-step new-session flow (host selection, agent kind selection,
 * folder navigation, and session creation) behind [SessionTransport].
 */
class NewSessionController(
    private val transport: SessionTransport = RelaySessionTransport,
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default),
    initialHostId: String = "",
    initialStep: Int = if (initialHostId.isBlank()) 0 else 1
) {
    private val _state = MutableStateFlow(
        NewSessionUiState(
            step = initialStep,
            selectedHostId = initialHostId
        )
    )
    val state: StateFlow<NewSessionUiState> = _state.asStateFlow()

    private var optionsJob: Job? = null
    private var foldersJob: Job? = null

    init {
        if (initialHostId.isNotBlank()) {
            loadOptions(initialHostId)
        }
    }

    fun syncStartableHosts(startableHosts: List<HostInfo>, preferredHost: String = "") {
        val currentHostId = _state.value.selectedHostId
        if (preferredHost.isNotBlank() && startableHosts.any { it.id == preferredHost }) {
            if (currentHostId != preferredHost) {
                selectHost(preferredHost)
            }
        } else if (startableHosts.none { it.id == currentHostId }) {
            val fallback = startableHosts.firstOrNull()?.id.orEmpty()
            if (currentHostId != fallback) {
                selectHost(fallback)
            }
        }
    }

    fun selectHost(hostId: String) {
        if (hostId.isBlank()) {
            _state.update {
                it.copy(
                    selectedHostId = "",
                    options = null,
                    optionsError = null,
                    selectedKind = "",
                    viewPath = "",
                    folderListing = null,
                    loadingFolders = false,
                    foldersError = null,
                    chosenPath = "",
                    label = "",
                    createError = null
                )
            }
            return
        }
        _state.update {
            it.copy(
                selectedHostId = hostId,
                options = null,
                optionsError = null,
                selectedKind = "",
                viewPath = "",
                folderListing = null,
                loadingFolders = false,
                foldersError = null,
                chosenPath = "",
                label = "",
                createError = null
            )
        }
        loadOptions(hostId)
    }

    private fun loadOptions(hostId: String) {
        optionsJob?.cancel()
        optionsJob = scope.launch {
            transport.loadSessionOptions(hostId).fold(
                onSuccess = { loaded ->
                    val defaultKind = sessionKinds(loaded).firstOrNull().orEmpty()
                    _state.update {
                        it.copy(
                            options = loaded,
                            optionsError = null,
                            selectedKind = defaultKind
                        )
                    }
                    if (loaded.roots.isNotEmpty()) {
                        loadFolders(hostId, path = "")
                    }
                },
                onFailure = { error ->
                    _state.update {
                        it.copy(
                            options = null,
                            optionsError = classifyRelayError(error)
                        )
                    }
                }
            )
        }
    }

    fun selectKind(kind: String) {
        _state.update { it.copy(selectedKind = kind, createError = null) }
    }

    fun navigateToFolder(path: String) {
        _state.update { it.copy(viewPath = path) }
        loadFolders(_state.value.selectedHostId, path)
    }

    fun chooseFolder(path: String) {
        _state.update {
            it.copy(
                chosenPath = path,
                label = if (it.label.isBlank()) defaultSessionLabel(path) else it.label,
                createError = null
            )
        }
    }

    fun setLabel(text: String) {
        _state.update { it.copy(label = text) }
    }

    fun setStep(step: Int) {
        _state.update { it.copy(step = step) }
    }

    private fun loadFolders(hostId: String, path: String) {
        foldersJob?.cancel()
        foldersJob = scope.launch {
            _state.update { it.copy(loadingFolders = true, foldersError = null) }
            transport.listFolders(hostId, path.ifBlank { null }).fold(
                onSuccess = { listing ->
                    _state.update {
                        it.copy(
                            folderListing = listing,
                            loadingFolders = false,
                            foldersError = null
                        )
                    }
                },
                onFailure = { error ->
                    _state.update {
                        it.copy(
                            folderListing = null,
                            loadingFolders = false,
                            foldersError = classifyRelayError(error)
                        )
                    }
                }
            )
        }
    }

    suspend fun createSession(): Result<SessionCreated> {
        val current = _state.value
        if (!canCreateSession(current.selectedHostId, current.selectedKind, current.chosenPath, current.creating)) {
            val err = RelayError("invalid_request", "Cannot create session", 400)
            return Result.failure(err)
        }
        _state.update { it.copy(creating = true, createError = null) }
        val title = current.label.trim().ifEmpty { null }
        val result = transport.createSession(current.selectedHostId, current.selectedKind, current.chosenPath, title)
        result.fold(
            onSuccess = {
                _state.update { it.copy(creating = false) }
            },
            onFailure = { error ->
                _state.update {
                    it.copy(
                        creating = false,
                        createError = classifyRelayError(error)
                    )
                }
            }
        )
        return result
    }

    fun close() {
        optionsJob?.cancel()
        foldersJob?.cancel()
    }
}
