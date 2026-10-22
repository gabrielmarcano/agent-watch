package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.mobile.ui.logic.needsAnswer
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.network.UiState

/**
 * The machines the relay knows: every host, whether it is online and reaches its herdr, what it runs,
 * and the projects (the roots its bridge allows) where a session can be started.
 *
 * The owner's other machine (an Ubuntu server with herdr of its own) shows up here as soon as it is
 * registered on the relay and runs the bridge (phase 8, step 3). Until then it is offline, which is
 * the truth.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MachinesScreen(
    state: UiState,
    onOpenChats: (String) -> Unit,
    onNewSession: (String) -> Unit
) {
    Scaffold { padding ->
        if (state.hosts.isEmpty()) {
            Column(
                modifier = Modifier.fillMaxSize().padding(padding).padding(24.dp),
                verticalArrangement = Arrangement.Center,
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Text(
                    stringResource(R.string.machines_empty),
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            return@Scaffold
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            item(key = "title") { BigTitle(stringResource(R.string.machines_title)) }
            items(state.hosts, key = { it.id }) { host ->
                MachineCard(
                    host = host,
                    agents = state.agents.count { it.host == host.id },
                    waiting = state.agents.count { it.host == host.id && it.needsAnswer() },
                    onOpenChats = { onOpenChats(host.id) },
                    onNewSession = { onNewSession(host.id) }
                )
            }
        }
    }
}

/** One machine: its state, what it runs, its projects, and the two things to do with it. */
@Composable
private fun MachineCard(
    host: HostInfo,
    agents: Int,
    waiting: Int,
    onOpenChats: () -> Unit,
    onNewSession: () -> Unit
) {
    var roots by remember(host.id) { mutableStateOf<List<String>>(emptyList()) }
    var loaded by remember(host.id) { mutableStateOf(false) }

    // The projects are the roots the bridge on that machine allows (contracts §2.1).
    LaunchedEffect(host.id) {
        RelayRepository.sessionOptions(host.id).fold(
            onSuccess = { roots = it.roots },
            onFailure = { roots = emptyList() }
        )
        loaded = true
    }

    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    Icons.Filled.Computer,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary
                )
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        text = host.name.ifBlank { host.id },
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    Text(
                        text = machineState(host),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
            Text(
                text = stringResource(R.string.machines_agents, agents, waiting),
                style = MaterialTheme.typography.bodyMedium
            )
            if (loaded && roots.isNotEmpty()) {
                Text(
                    text = stringResource(R.string.machines_projects),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                roots.forEach { root ->
                    Text(
                        text = root,
                        style = MaterialTheme.typography.bodyMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = onOpenChats) { Text(stringResource(R.string.machines_chats)) }
                FilledTonalButton(onClick = onNewSession) { Text(stringResource(R.string.new_session)) }
            }
        }
    }
}

/** The machine's own state: connected, and its herdr answering. */
@Composable
private fun machineState(host: HostInfo): String = when {
    !host.online -> stringResource(R.string.machines_offline)
    !host.herdr_online -> stringResource(R.string.machines_herdr_stopped)
    else -> stringResource(R.string.machines_online)
}
