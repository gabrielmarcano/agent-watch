package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.DarkMode
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.NotificationsOff
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PhoneAndroid
import androidx.compose.material.icons.filled.Storage
import androidx.compose.material.icons.filled.SystemUpdate
import androidx.compose.material.icons.filled.Wifi
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.gabriel.agentwatch.mobile.MobileApplication
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.LocalThemePrefs
import com.gabriel.agentwatch.mobile.ui.theme.ThemePrefs
import com.gabriel.agentwatch.network.UiState
import com.gabriel.agentwatch.update.UpdateState

/**
 * The phone's settings, the way a messaging app draws them: a title, rows with an icon and a
 * chevron, and the destructive action at the end.
 *
 * The update lives here now: the card with the new build, the check and the install.
 */
@Composable
fun SettingsScreen(
    state: UiState,
    update: UpdateState,
    chatStore: ChatStore? = null,
    chatState: MobileChatState? = null,
    onUpdate: () -> Unit,
    onRetryUpdate: () -> Unit,
    onCancelUpdate: () -> Unit,
    onInstallUpdate: () -> Unit,
    onDismissUpdate: () -> Unit,
    onCheckUpdates: () -> Unit,
    onForgetPhone: () -> Unit
) {
    val themePrefs = LocalThemePrefs.current
    val context = LocalContext.current
    val prefs = remember(context) { Prefs(context) }
    val actualChatState = chatState ?: remember(context) { MobileApplication.chatState(context) }
    val chatSnapshot by actualChatState.state.collectAsStateWithLifecycle()
    var confirmForget by remember { mutableStateOf(false) }

    Scaffold { padding ->
        LazyColumn(
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 88.dp)
        ) {
            item(key = "title") { BigTitle(stringResource(R.string.settings_title)) }

            // The update, where the update check is.
            item(key = "update") {
                UpdateCard(
                    update = update,
                    onUpdate = onUpdate,
                    onRetry = onRetryUpdate,
                    onCancel = onCancelUpdate,
                    onInstall = onInstallUpdate,
                    onDismiss = onDismissUpdate
                )
            }
            if (update.phase == com.gabriel.agentwatch.update.UpdatePhase.IDLE ||
                update.phase == com.gabriel.agentwatch.update.UpdatePhase.UP_TO_DATE
            ) {
                item(key = "check") {
                    SettingsCard {
                        SettingsRow(
                            icon = Icons.Filled.SystemUpdate,
                            title = stringResource(R.string.settings_check_updates),
                            subtitle = stringResource(R.string.settings_version, com.gabriel.agentwatch.mobile.BuildConfig.VERSION_NAME),
                            onClick = onCheckUpdates
                        )
                    }
                }
            }

            item { SectionTitle(stringResource(R.string.settings_account)) }
            item {
                SettingsCard {
                    SettingsRow(
                        icon = Icons.Filled.Wifi,
                        title = stringResource(R.string.settings_relay),
                        subtitle = prefs.relayUrl
                    )
                    SettingsRow(
                        icon = Icons.Filled.PhoneAndroid,
                        title = stringResource(R.string.settings_device),
                        subtitle = prefs.deviceId.orEmpty()
                    )
                    SettingsRow(
                        icon = Icons.Filled.Computer,
                        title = stringResource(R.string.settings_machines),
                        subtitle = stringResource(R.string.settings_machines_hint, state.hosts.size)
                    )
                }
            }

            item { SectionTitle(stringResource(R.string.settings_appearance)) }
            item {
                SettingsCard {
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        FilterChip(
                            selected = themePrefs.themeMode == ThemePrefs.MODE_SYSTEM,
                            onClick = { themePrefs.themeMode = ThemePrefs.MODE_SYSTEM },
                            label = { Text(stringResource(R.string.settings_theme_system)) }
                        )
                        FilterChip(
                            selected = themePrefs.themeMode == ThemePrefs.MODE_LIGHT,
                            onClick = { themePrefs.themeMode = ThemePrefs.MODE_LIGHT },
                            label = { Text(stringResource(R.string.settings_theme_light)) }
                        )
                        FilterChip(
                            selected = themePrefs.themeMode == ThemePrefs.MODE_DARK,
                            onClick = { themePrefs.themeMode = ThemePrefs.MODE_DARK },
                            label = { Text(stringResource(R.string.settings_theme_dark)) }
                        )
                    }
                    SettingsRow(
                        icon = Icons.Filled.DarkMode,
                        title = stringResource(R.string.settings_dynamic_color),
                        subtitle = stringResource(R.string.settings_dynamic_color_hint),
                        trailing = {
                            Switch(
                                checked = themePrefs.dynamicColor,
                                onCheckedChange = { themePrefs.dynamicColor = it }
                            )
                        }
                    )
                }
            }

            item { SectionTitle(stringResource(R.string.settings_notifications)) }
            item {
                val muted = chatSnapshot.muted.toList().sorted()
                val hidden = chatSnapshot.hidden.toList().sorted()
                SettingsCard {
                    SettingsRow(
                        icon = Icons.Filled.Notifications,
                        title = stringResource(R.string.settings_muted),
                        subtitle = muted.size.toString()
                    )
                    muted.forEach { token ->
                        SettingsRow(
                            icon = Icons.Filled.NotificationsOff,
                            title = chatSnapshot.label(token),
                            trailing = {
                                TextButton(onClick = { actualChatState.unmute(token) }) {
                                    Text(stringResource(R.string.settings_unmute))
                                }
                            }
                        )
                    }
                    SettingsRow(
                        icon = Icons.Filled.Archive,
                        title = stringResource(R.string.settings_hidden),
                        subtitle = hidden.size.toString()
                    )
                    hidden.forEach { token ->
                        SettingsRow(
                            icon = Icons.AutoMirrored.Filled.Chat,
                            title = chatSnapshot.label(token),
                            trailing = {
                                TextButton(onClick = { actualChatState.unhide(token) }) {
                                    Text(stringResource(R.string.settings_restore))
                                }
                            }
                        )
                    }
                }
            }

            item { SectionTitle(stringResource(R.string.settings_data)) }
            item {
                SettingsCard {
                    SettingsRow(
                        icon = Icons.Filled.Storage,
                        title = stringResource(R.string.settings_connection),
                        subtitle = if (state.stale) {
                            stringResource(R.string.notice_connecting)
                        } else {
                            stringResource(R.string.settings_connected)
                        }
                    )
                    SettingsRow(
                        icon = Icons.Filled.Person,
                        title = stringResource(R.string.settings_forget),
                        subtitle = stringResource(R.string.settings_forget_hint),
                        onClick = { confirmForget = true }
                    )
                }
            }
        }
    }

    if (confirmForget) {
        AlertDialog(
            onDismissRequest = { confirmForget = false },
            title = { Text(stringResource(R.string.settings_forget)) },
            text = { Text(stringResource(R.string.settings_forget_hint)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmForget = false
                    onForgetPhone()
                }) { Text(stringResource(R.string.confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmForget = false }) {
                    Text(stringResource(R.string.cancel_prompt))
                }
            }
        )
    }
}

/** A section's title, in the accent colour. */
@Composable
fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 24.dp, top = 16.dp, end = 16.dp, bottom = 6.dp)
    )
}

/** A rounded surface container grouping related settings rows. */
@Composable
private fun SettingsCard(content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 2.dp),
        shape = RoundedCornerShape(20.dp),
        color = MaterialTheme.colorScheme.surfaceContainerLow
    ) {
        Column(modifier = Modifier.padding(vertical = 4.dp), content = content)
    }
}

/** A settings row: an icon, the name, a hint under it, and a chevron when it opens something. */
@Composable
fun SettingsRow(
    icon: ImageVector,
    title: String,
    subtitle: String? = null,
    onClick: (() -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Box(
            modifier = Modifier
                .size(38.dp)
                .background(MaterialTheme.colorScheme.primaryContainer, CircleShape),
            contentAlignment = Alignment.Center
        ) {
            Icon(
                icon,
                contentDescription = null,
                modifier = Modifier.size(20.dp),
                tint = MaterialTheme.colorScheme.onPrimaryContainer
            )
        }
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            subtitle?.takeIf { it.isNotBlank() }?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )
            }
        }
        trailing?.let {
            Spacer(Modifier.width(12.dp))
            it()
        }
        if (onClick != null && trailing == null) {
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
    }
}
