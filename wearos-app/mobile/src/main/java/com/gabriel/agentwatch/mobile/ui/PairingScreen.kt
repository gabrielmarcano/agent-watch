package com.gabriel.agentwatch.mobile.ui

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.mobile.BuildConfig
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.logic.cleanPairCode
import com.gabriel.agentwatch.mobile.ui.logic.pairCodeComplete
import com.gabriel.agentwatch.mobile.ui.logic.pairDeviceName
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.network.RelayClient
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.logic.displayHost
import com.gabriel.agentwatch.ui.logic.isAcceptableRelayUrl
import com.gabriel.agentwatch.ui.logic.normalizeRelayUrl
import kotlinx.coroutines.launch

/**
 * Pairing with a 6-digit code from `agent-watch-bridge pair`. The relay URL starts from the saved one
 * or the build's shared-config default ([BuildConfig.DEFAULT_RELAY_URL], may be empty).
 *
 * [revoked]: the relay rejected the old token. The phone gets its own device token on the relay, so
 * pairing here never disturbs the watch's.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PairingScreen(revoked: Boolean, onPaired: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    val scope = rememberCoroutineScope()

    var relayUrl by rememberSaveable { mutableStateOf(prefs.relayUrl.ifBlank { BuildConfig.DEFAULT_RELAY_URL }) }
    var code by rememberSaveable { mutableStateOf("") }
    var pairing by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    val urlOk = isAcceptableRelayUrl(relayUrl, allowCleartext = BuildConfig.DEBUG)

    fun pair() {
        pairing = true
        error = null
        val url = normalizeRelayUrl(relayUrl)
        scope.launch {
            val deviceName = pairDeviceName(Build.MANUFACTURER, Build.MODEL)
            RelayClient(baseUrl = url).pair(code = code, deviceName = deviceName).fold(
                onSuccess = { resp ->
                    prefs.relayUrl = url
                    prefs.deviceId = resp.device_id
                    prefs.deviceToken = resp.device_token
                    // A full restart with the new pairing: closes the old stream, drops its data.
                    RelayRepository.restart(context)
                    onPaired()
                },
                onFailure = { error = commandErrorFeedback(it).message }
            )
            pairing = false
        }
    }

    Scaffold(topBar = { TopAppBar(title = { Text(stringResource(R.string.pair_title)) }) }) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            if (revoked) {
                Text(
                    stringResource(R.string.session_expired),
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodyMedium
                )
            }
            Text(
                stringResource(R.string.pair_intro),
                color = OnSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium
            )
            OutlinedTextField(
                value = relayUrl,
                onValueChange = { relayUrl = it; error = null },
                label = { Text(stringResource(R.string.relay_url)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                isError = relayUrl.isNotBlank() && !urlOk,
                supportingText = {
                    val text = when {
                        relayUrl.isBlank() -> stringResource(R.string.relay_url_missing)
                        !urlOk -> stringResource(R.string.relay_url_invalid)
                        else -> displayHost(normalizeRelayUrl(relayUrl))
                    }
                    Text(text)
                },
                modifier = Modifier.fillMaxWidth()
            )
            OutlinedTextField(
                value = code,
                onValueChange = { code = cleanPairCode(it); error = null },
                label = { Text(stringResource(R.string.pair_code)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                supportingText = {
                    Text(
                        when {
                            code.isEmpty() -> stringResource(R.string.pair_code_missing)
                            !pairCodeComplete(code) -> stringResource(R.string.pair_code_incomplete)
                            else -> code.chunked(3).joinToString(" ")
                        }
                    )
                },
                modifier = Modifier.fillMaxWidth()
            )
            Button(
                onClick = ::pair,
                enabled = !pairing && urlOk && pairCodeComplete(code),
                modifier = Modifier.fillMaxWidth()
            ) {
                Text(stringResource(if (pairing) R.string.pairing else R.string.pair))
            }
            error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}
