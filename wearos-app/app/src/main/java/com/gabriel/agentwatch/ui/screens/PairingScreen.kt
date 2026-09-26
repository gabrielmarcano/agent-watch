package com.gabriel.agentwatch.ui.screens

import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.wear.compose.material3.EdgeButton
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.BuildConfig
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.commandErrorFeedback
import com.gabriel.agentwatch.data.Prefs
import com.gabriel.agentwatch.network.RelayClient
import com.gabriel.agentwatch.network.RelayRepository
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.TextInput
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.logic.isAcceptableRelayUrl
import com.gabriel.agentwatch.ui.logic.normalizeRelayUrl
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import kotlinx.coroutines.launch

private const val CODE_LENGTH = 6

/**
 * Pairing with a 6-digit code from `agent-watch-bridge pair`. The relay URL starts from the saved one
 * or the build's shared-config default ([BuildConfig.DEFAULT_RELAY_URL], may be empty) and is edited
 * with the system keyboard. [revoked]: the relay rejected the old token.
 */
@Composable
fun PairingScreen(revoked: Boolean, onPaired: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    val scope = rememberCoroutineScope()

    var relayUrl by remember { mutableStateOf(prefs.relayUrl.ifBlank { BuildConfig.DEFAULT_RELAY_URL }) }
    var code by remember { mutableStateOf("") }
    var pairing by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    val urlOk = isAcceptableRelayUrl(relayUrl, allowCleartext = BuildConfig.DEBUG)
    val urlLabel = stringResource(R.string.relay_url)
    val codeLabel = stringResource(R.string.pair_code)

    val urlInput = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        TextInput.result(result.data)?.let {
            relayUrl = normalizeRelayUrl(it)
            error = null
        }
    }
    val codeInput = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        TextInput.result(result.data)?.let {
            code = it.filter(Char::isDigit).take(CODE_LENGTH)
            error = null
        }
    }

    fun pair() {
        pairing = true
        error = null
        scope.launch {
            val deviceName = "${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} ${Build.MODEL}"
            RelayClient(baseUrl = relayUrl).pair(code = code, deviceName = deviceName).fold(
                onSuccess = { resp ->
                    prefs.relayUrl = relayUrl
                    prefs.deviceId = resp.device_id
                    prefs.deviceToken = resp.device_token
                    // FcmRegistrar registers push when the new stream opens.
                    RelayRepository.restart(context)
                    onPaired()
                },
                onFailure = { error = commandErrorFeedback(it).message }
            )
            pairing = false
        }
    }

    ScreenList(
        edgeButton = {
            EdgeButton(onClick = ::pair, enabled = !pairing && urlOk && code.length == CODE_LENGTH) {
                Text(stringResource(if (pairing) R.string.pairing else R.string.pair))
            }
        }
    ) { spec ->
        item(key = "title") {
            Column(transformedItem(spec).padding(start = 18.dp, end = 18.dp, top = 6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                if (revoked) {
                    Text(
                        stringResource(R.string.session_expired),
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.labelMedium,
                        textAlign = TextAlign.Center
                    )
                }
                Text(stringResource(R.string.pair_title), style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
                Text(
                    stringResource(R.string.pair_hint),
                    color = OnSurfaceVariant,
                    style = MaterialTheme.typography.bodySmall,
                    textAlign = TextAlign.Center
                )
            }
        }
        item(key = "url") {
            FilledTonalButton(
                onClick = { urlInput.launch(TextInput.intent(urlLabel)) },
                enabled = !pairing,
                modifier = Modifier.fillMaxWidth().padding(top = 6.dp).transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_edit, null, MaterialTheme.colorScheme.onSurface) },
                secondaryLabel = {
                    Text(
                        relayUrl.ifBlank { stringResource(R.string.relay_url_missing) },
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                },
                label = { Text(urlLabel) }
            )
        }
        item(key = "code") {
            FilledTonalButton(
                onClick = { codeInput.launch(TextInput.intent(codeLabel)) },
                enabled = !pairing,
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                icon = { ResIcon(R.drawable.ic_link, null, MaterialTheme.colorScheme.onSurface) },
                secondaryLabel = {
                    Text(
                        if (code.isEmpty()) stringResource(R.string.pair_code_missing)
                        else code.chunked(3).joinToString(" ")
                    )
                },
                label = { Text(codeLabel) }
            )
        }
        val badUrl = relayUrl.isNotBlank() && !urlOk
        if (error != null || badUrl) {
            item(key = "error") {
                Text(
                    error ?: stringResource(R.string.relay_url_invalid),
                    modifier = transformedItem(spec),
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.labelMedium,
                    textAlign = TextAlign.Center
                )
            }
        }
        if (code.isNotEmpty() && code.length < CODE_LENGTH && error == null) {
            item(key = "incomplete") {
                Text(
                    stringResource(R.string.pair_code_incomplete),
                    modifier = transformedItem(spec),
                    color = OnSurfaceVariant,
                    style = MaterialTheme.typography.labelMedium,
                    textAlign = TextAlign.Center
                )
            }
        }
    }
}
