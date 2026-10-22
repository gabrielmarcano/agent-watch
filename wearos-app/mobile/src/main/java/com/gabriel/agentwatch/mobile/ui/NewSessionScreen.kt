package com.gabriel.agentwatch.mobile.ui

import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.canCreateSession
import com.gabriel.agentwatch.mobile.ui.logic.defaultSessionLabel
import com.gabriel.agentwatch.mobile.ui.logic.sessionHosts
import com.gabriel.agentwatch.mobile.ui.logic.sessionKinds
import com.gabriel.agentwatch.model.FolderListResponse
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.SessionCreated
import com.gabriel.agentwatch.model.SessionOptionsResponse
import com.gabriel.agentwatch.network.RelayError
import com.gabriel.agentwatch.network.RelayRepository
import kotlinx.coroutines.launch

/** The relay code of a failed session request, as the form's one-line error. */
@StringRes
private fun sessionErrorRes(error: Throwable): Int = when ((error as? RelayError)?.code) {
    "not_allowed" -> R.string.session_err_not_allowed
    "host_offline" -> R.string.session_err_host_offline
    "herdr_offline" -> R.string.session_err_herdr_offline
    "timeout" -> R.string.session_err_timeout
    "not_paired", "unauthorized" -> R.string.session_expired
    else -> R.string.session_err_generic
}

/**
 * Start a new agent where the owner wants it: pick the device, the agent kind and a folder inside
 * that bridge's configured session roots, then optionally name it. The bridge creates the workspace
 * (or reuses the folder's) and starts the agent; the phone opens its chat as soon as herdr lists it.
 *
 * Only hosts whose bridge and herdr are up are offered ([sessionHosts]): a session cannot start
 * anywhere else. A host with no configured roots says so instead of listing a filesystem.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewSessionScreen(
    hosts: List<HostInfo>,
    startHost: String = "",
    onBack: () -> Unit,
    onCreated: (SessionCreated) -> Unit
) {
    val scope = rememberCoroutineScope()
    val startable = remember(hosts) { sessionHosts(hosts) }

    var hostId by rememberSaveable { mutableStateOf("") }
    var options by remember { mutableStateOf<SessionOptionsResponse?>(null) }
    var optionsError by remember { mutableStateOf<Int?>(null) }
    var kind by rememberSaveable { mutableStateOf("") }
    var viewPath by rememberSaveable { mutableStateOf("") }
    var listing by remember { mutableStateOf<FolderListResponse?>(null) }
    var foldersError by remember { mutableStateOf<Int?>(null) }
    var loadingFolders by remember { mutableStateOf(false) }
    var chosen by rememberSaveable { mutableStateOf("") }
    var label by rememberSaveable { mutableStateOf("") }
    var creating by remember { mutableStateOf(false) }
    var createError by remember { mutableStateOf<Int?>(null) }
    // Three steps, the way a messenger asks for a contact: the machine, the agent, the folder.
    var step by rememberSaveable { mutableStateOf(if (startHost.isBlank()) 0 else 1) }

    // A host that went offline cannot stay selected; the machine the caller asked for wins, else
    // the first startable one.
    LaunchedEffect(startable, startHost) {
        if (startHost.isNotBlank() && startable.any { it.id == startHost }) {
            hostId = startHost
        } else if (startable.none { it.id == hostId }) {
            hostId = startable.firstOrNull()?.id.orEmpty()
        }
    }

    // What the chosen host allows: kinds and roots. Both reset with the host.
    LaunchedEffect(hostId) {
        if (hostId.isBlank()) return@LaunchedEffect
        options = null
        optionsError = null
        kind = ""
        viewPath = ""
        chosen = ""
        label = ""
        createError = null
        RelayRepository.sessionOptions(hostId)
            .onSuccess { loaded ->
                options = loaded
                kind = sessionKinds(loaded).firstOrNull().orEmpty()
            }
            .onFailure { optionsError = sessionErrorRes(it) }
    }

    // The folder being browsed, or the roots when nothing is open yet.
    LaunchedEffect(hostId, viewPath, options) {
        val current = options ?: return@LaunchedEffect
        if (hostId.isBlank() || current.roots.isEmpty()) {
            listing = null
            loadingFolders = false
            return@LaunchedEffect
        }
        loadingFolders = true
        foldersError = null
        RelayRepository.folders(hostId, viewPath.ifBlank { null })
            .onSuccess { listing = it }
            .onFailure {
                listing = null
                foldersError = sessionErrorRes(it)
            }
        loadingFolders = false
    }

    fun choose(path: String) {
        chosen = path
        createError = null
    }

    fun create() {
        if (!canCreateSession(hostId, kind, chosen, creating)) return
        creating = true
        createError = null
        val name = label.trim().ifEmpty { null }
        scope.launch {
            val res = RelayRepository.createSession(hostId, kind, chosen, name)
            creating = false
            res.onSuccess(onCreated).onFailure { createError = sessionErrorRes(it) }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(stepTitle(step))) },
                navigationIcon = {
                    IconButton(onClick = { if (step > 0) step-- else onBack() }) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.back)
                        )
                    }
                }
            )
        },
        bottomBar = {
            if (step == 2) {
                CreateBar(
                    label = label,
                    onLabelChange = { label = it; createError = null },
                    hint = defaultSessionLabel(chosen),
                    enabled = canCreateSession(hostId, kind, chosen, creating),
                    creating = creating,
                    error = createError,
                    onCreate = ::create
                )
            }
        }
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            when {
                startable.isEmpty() ->
                    Centered(stringResource(R.string.session_no_hosts), MaterialTheme.colorScheme.onSurfaceVariant)
                optionsError != null && step > 0 ->
                    Centered(stringResource(optionsError!!), MaterialTheme.colorScheme.error)
                step == 0 -> MachineStep(
                    hosts = startable,
                    selected = hostId,
                    onSelect = {
                        hostId = it
                        step = 1
                    }
                )
                step == 1 -> {
                    val kinds = sessionKinds(options)
                    if (kinds.isEmpty()) {
                        Centered(stringResource(R.string.session_not_configured), MaterialTheme.colorScheme.onSurfaceVariant)
                    } else {
                        KindStep(
                            kinds = kinds,
                            selected = kind,
                            machine = startable.firstOrNull { it.id == hostId }?.name.orEmpty(),
                            onSelect = {
                                kind = it
                                step = 2
                            }
                        )
                    }
                }
                else -> {
                    if (options != null && options!!.roots.isEmpty()) {
                        Text(
                            text = stringResource(R.string.session_not_configured),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.bodyMedium,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)
                        )
                    } else {
                        ChosenFolder(chosen)
                        FolderBrowser(
                            listing = listing,
                            loading = loadingFolders,
                            error = foldersError,
                            chosen = chosen,
                            modifier = Modifier.weight(1f),
                            onOpen = { viewPath = it },
                            onChoose = ::choose
                        )
                    }
                }
            }
        }
    }
}

/** The step's title: the machine, the agent, the folder. */
private fun stepTitle(step: Int): Int = when (step) {
    0 -> R.string.session_step_machine
    1 -> R.string.session_step_agent
    else -> R.string.session_step_folder
}

/** Step one: the machine the session runs on. */
@Composable
private fun MachineStep(hosts: List<HostInfo>, selected: String, onSelect: (String) -> Unit) {
    LazyColumn(Modifier.fillMaxSize()) {
        items(hosts, key = { it.id }) { host ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { onSelect(host.id) }
                    .padding(horizontal = 16.dp, vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(Modifier.weight(1f)) {
                    Text(host.name.ifBlank { host.id }, style = MaterialTheme.typography.bodyLarge)
                    Text(
                        text = if (host.online) {
                            stringResource(R.string.machines_online)
                        } else {
                            stringResource(R.string.machines_offline)
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                if (host.id == selected) {
                    Icon(
                        Icons.Filled.Check,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.primary
                    )
                }
            }
        }
    }
}

/** Step two: the agent kind the machine may start. */
@Composable
private fun KindStep(kinds: List<String>, selected: String, machine: String, onSelect: (String) -> Unit) {
    Column(Modifier.fillMaxSize().padding(horizontal = 16.dp, vertical = 8.dp)) {
        if (machine.isNotBlank()) {
            Text(
                text = stringResource(R.string.session_on_machine, machine),
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        kinds.forEach { kind ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { onSelect(kind) }
                    .padding(vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(kind, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
                if (kind == selected) {
                    Icon(
                        Icons.Filled.Check,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.primary
                    )
                }
            }
        }
    }
}

@Composable
private fun HostChips(hosts: List<HostInfo>, selected: String, onSelect: (String) -> Unit) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        items(hosts, key = { it.id }) { host ->
            FilterChip(
                selected = selected == host.id,
                onClick = { onSelect(host.id) },
                label = { Text(host.name.ifBlank { host.id }, maxLines = 1) }
            )
        }
    }
}

@Composable
private fun KindChips(kinds: List<String>, selected: String, onSelect: (String) -> Unit) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        items(kinds, key = { it }) { kind ->
            FilterChip(
                selected = selected == kind,
                onClick = { onSelect(kind) },
                label = { Text(kind) }
            )
        }
    }
}

@Composable
private fun ChosenFolder(path: String) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
        Text(
            text = stringResource(R.string.session_folder),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = path.ifBlank { stringResource(R.string.session_folder_none) },
            style = MaterialTheme.typography.bodyMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

/**
 * The folders inside the selected root: [listing] is the roots when its [FolderListResponse.path]
 * is empty. Tapping a folder opens it; its "Use" button, or the bar under the list, chooses it.
 */
@Composable
private fun FolderBrowser(
    listing: FolderListResponse?,
    loading: Boolean,
    error: Int?,
    chosen: String,
    modifier: Modifier = Modifier,
    onOpen: (String) -> Unit,
    onChoose: (String) -> Unit
) {
    when {
        error != null -> Centered(stringResource(error), MaterialTheme.colorScheme.error, modifier)
        loading && listing == null -> Box(modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(Modifier.size(24.dp))
        }
        listing == null -> Unit
        else -> LazyColumn(modifier.fillMaxWidth()) {
            listing.parent?.takeIf { it.isNotBlank() }?.let { parent ->
                item(key = "up") {
                    FolderRow(
                        name = "..",
                        path = parent,
                        chosen = false,
                        up = true,
                        onOpen = { onOpen(parent) },
                        onChoose = null
                    )
                }
            }
            if (listing.path.isNotBlank()) {
                item(key = "use") {
                    ChosenBar(
                        path = listing.path,
                        chosen = chosen == listing.path,
                        onChoose = { onChoose(listing.path) }
                    )
                }
            }
            if (listing.entries.isEmpty()) {
                item(key = "empty") {
                    Text(
                        text = stringResource(R.string.session_empty_folder),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)
                    )
                }
            }
            items(listing.entries, key = { it.path }) { entry ->
                FolderRow(
                    name = entry.name,
                    path = entry.path,
                    chosen = chosen == entry.path,
                    up = false,
                    onOpen = { onOpen(entry.path) },
                    onChoose = { onChoose(entry.path) }
                )
            }
        }
    }
}

@Composable
private fun FolderRow(
    name: String,
    path: String,
    chosen: Boolean,
    up: Boolean,
    onOpen: () -> Unit,
    onChoose: (() -> Unit)?
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(start = 16.dp, end = 8.dp, top = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        if (up) {
            Icon(
                Icons.Filled.KeyboardArrowUp,
                contentDescription = stringResource(R.string.session_up),
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(20.dp)
            )
            Spacer(Modifier.width(8.dp))
        }
        Text(
            text = name,
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f)
        )
        if (onChoose != null) {
            TextButton(onClick = onChoose) {
                if (chosen) {
                    Icon(Icons.Filled.Check, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(4.dp))
                    Text(stringResource(R.string.session_selected))
                } else {
                    Text(stringResource(R.string.session_use))
                }
            }
        }
    }
}

/** "Use this folder" under the list: the folder being browsed is usually the one wanted. */
@Composable
private fun ChosenBar(path: String, chosen: Boolean, onChoose: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 4.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.surface)
            .padding(horizontal = 12.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            text = path,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f)
        )
        TextButton(onClick = onChoose, enabled = !chosen) {
            Text(stringResource(if (chosen) R.string.session_selected else R.string.session_use_folder))
        }
    }
}

@Composable
private fun CreateBar(
    label: String,
    onLabelChange: (String) -> Unit,
    hint: String,
    enabled: Boolean,
    creating: Boolean,
    error: Int?,
    onCreate: () -> Unit
) {
    Surface(color = MaterialTheme.colorScheme.surface, shadowElevation = 8.dp) {
        Column(Modifier.fillMaxWidth().imePadding().navigationBarsPadding()) {
            error?.let {
                Text(
                    text = stringResource(it),
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)
                )
            }
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                OutlinedTextField(
                    value = label,
                    onValueChange = onLabelChange,
                    modifier = Modifier.weight(1f),
                    placeholder = { Text(stringResource(R.string.session_name_hint, hint)) },
                    singleLine = true,
                    shape = RoundedCornerShape(24.dp)
                )
                Spacer(Modifier.width(8.dp))
                Button(onClick = onCreate, enabled = enabled) {
                    if (creating) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(18.dp),
                            strokeWidth = 2.dp,
                            color = MaterialTheme.colorScheme.onPrimary
                        )
                    } else {
                        Text(stringResource(R.string.session_create))
                    }
                }
            }
        }
    }
}

/** The wait before the new agent shows up in the list: the bridge is starting it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StartingSessionScreen(label: String, onBack: () -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.new_session)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.back)
                        )
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().padding(padding),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            CircularProgressIndicator()
            Spacer(Modifier.height(16.dp))
            Text(
                text = label,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(top = 16.dp)
            )
            Text(
                text = stringResource(R.string.starting_session_hint),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(horizontal = 24.dp, vertical = 4.dp)
            )
        }
    }
}

@Composable
private fun Centered(
    text: String,
    color: androidx.compose.ui.graphics.Color,
    modifier: Modifier = Modifier
) {
    Box(modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(text, color = color, style = MaterialTheme.typography.bodyMedium)
    }
}
