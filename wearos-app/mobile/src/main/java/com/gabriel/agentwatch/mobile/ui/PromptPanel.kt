package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.approval.morePermissionOptions
import com.gabriel.agentwatch.approval.permissionButtons
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.ui.logic.tailPreview

private const val SCREEN_TAIL_LINES = 12

/**
 * The prompt waiting for an answer, as the chat shows it: an inline card from the agent, answered in
 * place (wearos-app/ARCHITECTURE.md §4b — roles decide the buttons, never positions; the green Allow
 * only maps to `allow_once`; `allow_always` asks for confirmation; an `unknown` prompt shows its
 * screen tail and offers only Cancel).
 */
@Composable
fun PromptPanel(
    prompt: PendingPrompt,
    locked: Boolean,
    onOption: (PromptOption) -> Unit,
    onCancel: () -> Unit
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            text = stringResource(
                when (prompt.kind) {
                    "permission" -> R.string.prompt_permission
                    "question" -> R.string.prompt_question
                    else -> R.string.prompt_unknown
                }
            ),
            color = OnSurfaceVariant,
            style = MaterialTheme.typography.labelLarge
        )
        if (prompt.title.isNotBlank()) {
            Text(prompt.title, style = MaterialTheme.typography.titleMedium)
        }
        prompt.detail?.takeIf { it.isNotBlank() }?.let {
            Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
        when (prompt.kind) {
            "permission" -> PermissionActions(prompt, locked, onOption, onCancel)
            "question" -> QuestionActions(prompt, locked, onOption)
            else -> UnknownPromptActions(prompt, locked, onCancel)
        }
    }
}

@Composable
private fun PermissionActions(
    prompt: PendingPrompt,
    locked: Boolean,
    onOption: (PromptOption) -> Unit,
    onCancel: () -> Unit
) {
    val buttons = permissionButtons(prompt)
    val deny = buttons.deny
    val allow = buttons.allow
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        // The positive action sits on the right, and only "allow_once" ever fills it.
        if (deny != null) {
            OutlinedButton(
                onClick = { onOption(deny) },
                enabled = !locked,
                modifier = Modifier.weight(1f)
            ) { Text(deny.label.ifBlank { stringResource(R.string.deny) }) }
        } else {
            OutlinedButton(onClick = onCancel, enabled = !locked, modifier = Modifier.weight(1f)) {
                Text(stringResource(R.string.cancel_prompt))
            }
        }
        allow?.let {
            Button(onClick = { onOption(it) }, enabled = !locked, modifier = Modifier.weight(1f)) {
                Text(it.label.ifBlank { stringResource(R.string.allow) })
            }
        }
    }

    val more = morePermissionOptions(prompt)
    if (more.isNotEmpty()) {
        Text(
            stringResource(R.string.more_options),
            color = OnSurfaceVariant,
            style = MaterialTheme.typography.labelMedium
        )
        more.forEach { option ->
            OutlinedButton(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(horizontalAlignment = Alignment.Start) {
                    Text(option.label)
                    option.description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = OnSurfaceVariant)
                    }
                }
            }
        }
    }
}

@Composable
private fun QuestionActions(prompt: PendingPrompt, locked: Boolean, onOption: (PromptOption) -> Unit) {
    // A question's options are its answers; Cancel is never offered for one.
    prompt.options.forEach { option ->
        val content: @Composable () -> Unit = {
            Column(horizontalAlignment = Alignment.Start) {
                Text(option.label)
                option.description?.takeIf { it.isNotBlank() }?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        if (option.role == "deny") {
            OutlinedButton(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) { content() }
        } else {
            Button(
                onClick = { onOption(option) },
                enabled = !locked,
                modifier = Modifier.fillMaxWidth()
            ) { content() }
        }
    }
}

@Composable
private fun UnknownPromptActions(prompt: PendingPrompt, locked: Boolean, onCancel: () -> Unit) {
    prompt.raw_tail?.takeIf { it.isNotBlank() }?.let {
        Text(
            text = tailPreview(it, SCREEN_TAIL_LINES).text,
            fontFamily = FontFamily.Monospace,
            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.padding(vertical = 4.dp)
        )
    }
    Text(
        stringResource(R.string.answer_on_device),
        color = OnSurfaceVariant,
        style = MaterialTheme.typography.bodySmall
    )
    OutlinedButton(onClick = onCancel, enabled = !locked) { Text(stringResource(R.string.cancel_prompt)) }
}
