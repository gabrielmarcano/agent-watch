package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.mobile.BuildConfig
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.LocalThemePrefs
import com.gabriel.agentwatch.mobile.ui.theme.ThemePrefs
import com.gabriel.agentwatch.network.UiState

/**
 * The phone's settings: the relay and the pairing, the look, the notifications and the version.
 *
 * The look is the phone's own (docs/phases/7-android-ux.md § 9): the system palette on Android 12+
 * and light or dark, both saved on the phone.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    state: UiState,
    mutedChats: MutedChats,
    onCheckUpdates: () -> Unit,
    onForgetPhone: () -> Unit
) {
    val themePrefs = LocalThemePrefs.current
    val context = LocalContext.current
    val prefs = remember(context) { Prefs(context) }
    var confirmForget by remember { mutableStateOf(false) }
    Scaffold(
        topBar = { TopAppBar(title = { Text(stringResource(R.string.settings_title)) }) }
    ) { padding ->
        LazyColumn(
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            item { SectionTitle(stringResource(R.string.settings_account)) }
            item {
                SettingRow(
                    title = stringResource(R.string.settings_relay),
                    subtitle = prefs.relayUrl
                )
            }
            item {
                SettingRow(
                    title = stringResource(R.string.settings_device),
                    subtitle = prefs.deviceId.orEmpty()
                )
            }
            item {
                TextButton(onClick = { confirmForget = true }) {
                    Text(stringResource(R.string.settings_forget), color = MaterialTheme.colorScheme.error)
                }
            }

            item { SectionTitle(stringResource(R.string.settings_appearance)) }
            item {
                Row(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
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
            }
            item {
                SettingRow(
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

            item { SectionTitle(stringResource(R.string.settings_notifications)) }
            item {
                SettingRow(
                    title = stringResource(R.string.settings_muted),
                    subtitle = mutedChats.count().toString()
                )
            }
            mutedChats.all().forEach { token ->
                item(key = "muted-$token") {
                    SettingRow(
                        title = mutedChats.label(token),
                        trailing = {
                            TextButton(onClick = { mutedChats.unmute(token) }) {
                                Text(stringResource(R.string.settings_unmute))
                            }
                        }
                    )
                }
            }

            item { SectionTitle(stringResource(R.string.settings_data)) }
            item {
                SettingRow(
                    title = stringResource(R.string.settings_check_updates),
                    subtitle = stringResource(R.string.settings_version, BuildConfig.VERSION_NAME),
                    trailing = {
                        OutlinedButton(onClick = onCheckUpdates) {
                            Text(stringResource(R.string.update_action))
                        }
                    }
                )
            }
            item {
                SettingRow(
                    title = stringResource(R.string.settings_connection),
                    subtitle = if (state.stale) {
                        stringResource(R.string.notice_connecting)
                    } else {
                        stringResource(R.string.settings_connected)
                    }
                )
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

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, top = 16.dp, end = 16.dp, bottom = 4.dp)
    )
}

@Composable
private fun SettingRow(
    title: String,
    subtitle: String? = null,
    trailing: (@Composable () -> Unit)? = null
) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
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
    }
}
