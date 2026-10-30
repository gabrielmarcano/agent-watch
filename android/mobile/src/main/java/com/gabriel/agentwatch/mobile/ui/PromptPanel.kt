package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.approval.morePermissionOptions
import com.gabriel.agentwatch.approval.permissionButtons
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.mobile.ui.theme.warningAccent
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.ui.logic.tailPreview

private const val SCREEN_TAIL_LINES = 12

/**
 * The prompt waiting for an answer, as the chat shows it: an inline card from the agent, answered in
 * place (android/ARCHITECTURE.md §4b — roles decide the buttons, never positions; the green Allow
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
    val accent = warningAccent()
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .size(8.dp)
                    .background(accent, CircleShape)
            )
            Spacer(Modifier.width(6.dp))
            Text(
                text = stringResource(
                    when (prompt.kind) {
                        "permission" -> R.string.prompt_permission
                        "question" -> R.string.prompt_question
                        else -> R.string.prompt_unknown
                    }
                ),
                color = accent,
                style = MaterialTheme.typography.labelLarge.copy(fontWeight = FontWeight.SemiBold)
            )
        }
        if (prompt.title.isNotBlank()) {
            Text(prompt.title, style = MaterialTheme.typography.titleMedium)
        }
        prompt.detail?.takeIf { it.isNotBlank() }?.let {
            Box(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(8.dp))
                    .background(MaterialTheme.colorScheme.surfaceContainerHigh)
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 10.dp, vertical = 6.dp)
            ) {
                Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
            }
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
        val denyInteraction = remember { MutableInteractionSource() }
        if (deny != null) {
            OutlinedButton(
                onClick = { onOption(deny) },
                enabled = !locked,
                interactionSource = denyInteraction,
                modifier = Modifier.weight(1f).pressScale(denyInteraction, 0.94f)
            ) { Text(deny.label.ifBlank { stringResource(R.string.deny) }) }
        } else {
            OutlinedButton(
                onClick = onCancel,
                enabled = !locked,
                interactionSource = denyInteraction,
                modifier = Modifier.weight(1f).pressScale(denyInteraction, 0.94f)
            ) {
                Text(stringResource(R.string.cancel_prompt))
            }
        }
        allow?.let {
            val allowInteraction = remember { MutableInteractionSource() }
            Button(
                onClick = { onOption(it) },
                enabled = !locked,
                interactionSource = allowInteraction,
                modifier = Modifier.weight(1f).pressScale(allowInteraction, 0.94f)
            ) {
                Text(it.label.ifBlank { stringResource(R.string.allow) })
            }
        }
    }

    val more = morePermissionOptions(prompt)
    if (more.isNotEmpty()) {
        Text(
            stringResource(R.string.more_options),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.labelMedium
        )
        more.forEach { option ->
            val optInteraction = remember(option.id) { MutableInteractionSource() }
            FilledTonalButton(
                onClick = { onOption(option) },
                enabled = !locked,
                interactionSource = optInteraction,
                modifier = Modifier.fillMaxWidth().pressScale(optInteraction, 0.96f)
            ) {
                Column(horizontalAlignment = Alignment.Start, modifier = Modifier.fillMaxWidth()) {
                    Text(option.label)
                    option.description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSecondaryContainer.copy(alpha = 0.8f))
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
        val optInteraction = remember(option.id) { MutableInteractionSource() }
        val content: @Composable () -> Unit = {
            Column(horizontalAlignment = Alignment.Start, modifier = Modifier.fillMaxWidth()) {
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
                interactionSource = optInteraction,
                modifier = Modifier.fillMaxWidth().pressScale(optInteraction, 0.96f)
            ) { content() }
        } else {
            FilledTonalButton(
                onClick = { onOption(option) },
                enabled = !locked,
                interactionSource = optInteraction,
                modifier = Modifier.fillMaxWidth().pressScale(optInteraction, 0.96f)
            ) { content() }
        }
    }
}

@Composable
private fun UnknownPromptActions(prompt: PendingPrompt, locked: Boolean, onCancel: () -> Unit) {
    prompt.raw_tail?.takeIf { it.isNotBlank() }?.let {
        Box(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .background(MaterialTheme.colorScheme.surfaceContainerHigh)
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 10.dp, vertical = 6.dp)
        ) {
            Text(
                text = tailPreview(it, SCREEN_TAIL_LINES).text,
                fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodySmall
            )
        }
    }
    Text(
        stringResource(R.string.answer_on_device),
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        style = MaterialTheme.typography.bodySmall
    )
    val cancelInteraction = remember { MutableInteractionSource() }
    OutlinedButton(
        onClick = onCancel,
        enabled = !locked,
        interactionSource = cancelInteraction,
        modifier = Modifier.pressScale(cancelInteraction, 0.94f)
    ) { Text(stringResource(R.string.cancel_prompt)) }
}
