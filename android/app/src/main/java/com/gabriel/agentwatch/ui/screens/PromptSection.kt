package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.foundation.lazy.TransformingLazyColumnScope
import androidx.wear.compose.material3.Button
import androidx.wear.compose.material3.ButtonDefaults
import androidx.wear.compose.material3.FilledTonalButton
import androidx.wear.compose.material3.ListSubHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.OutlinedButton
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.lazy.TransformationSpec
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.approval.CommandFeedback
import com.gabriel.agentwatch.approval.morePermissionOptions
import com.gabriel.agentwatch.approval.needsConfirmation
import com.gabriel.agentwatch.approval.permissionButtons
import com.gabriel.agentwatch.model.PendingPrompt
import com.gabriel.agentwatch.model.PromptOption
import com.gabriel.agentwatch.ui.components.ResIcon
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.logic.TextPreview
import com.gabriel.agentwatch.ui.logic.headPreview
import com.gabriel.agentwatch.ui.logic.tailPreview
import com.gabriel.agentwatch.ui.theme.Amber
import com.gabriel.agentwatch.ui.theme.Green
import com.gabriel.agentwatch.ui.theme.OnGreen
import com.gabriel.agentwatch.ui.theme.OnSurface
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.Red
import com.gabriel.agentwatch.ui.theme.SurfaceHigh
import com.gabriel.agentwatch.ui.theme.SurfaceLow

private const val PREVIEW_LINES = 6

/** What the prompt's buttons can do right now. */
data class PromptControls(
    /** A command is in flight or was accepted and the agent has not moved yet: buttons disabled. */
    val locked: Boolean,
    /** The relay accepted the answer; waiting for the agent to leave the prompt. */
    val sent: Boolean,
    /** The last command's error, shown next to the buttons. */
    val error: CommandFeedback?,
    val onAnswer: (PromptOption) -> Unit,
    /** Esc on the Mac: dismisses a question, or denies when a permission has no deny option. */
    val onCancel: () -> Unit,
    val onViewAll: (String) -> Unit
)

/**
 * The prompt as separate list items (kind, question, command, feedback, buttons), anchored under the
 * agent header. Options are resolved by role, never by position; ALLOW is only ever `allow_once`, and
 * "don't ask again" options go through [PromptControls.onAnswer] only after the screen confirms them.
 */
fun TransformingLazyColumnScope.promptItems(prompt: PendingPrompt, spec: TransformationSpec, controls: PromptControls) {
    item(key = "prompt-kind") {
        val (icon, label, color) = when (prompt.kind) {
            "permission" -> Triple(R.drawable.ic_status_blocked, R.string.prompt_permission, Amber)
            "question" -> Triple(R.drawable.ic_status_unknown, R.string.prompt_question, Amber)
            else -> Triple(R.drawable.ic_computer, R.string.prompt_unknown, Amber)
        }
        ListSubHeader(
            modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
            transformation = SurfaceTransformation(spec),
            icon = { ResIcon(icon, null, color, Modifier.size(18.dp)) }
        ) { Text(stringResource(label), color = color) }
    }

    if (prompt.title.isNotBlank()) {
        item(key = "prompt-title") {
            Text(
                prompt.title,
                modifier = transformedItem(spec),
                style = MaterialTheme.typography.titleMedium,
                textAlign = TextAlign.Center
            )
        }
    }

    // A permission shows a command (monospace, head); a question its text; unknown the screen tail,
    // whose last lines hold the question.
    val detail = prompt.detail?.takeIf { it.isNotBlank() }
    when {
        prompt.kind == "permission" && detail != null -> previewItems("prompt-detail", headPreview(detail, PREVIEW_LINES), detail, true, spec, controls)
        prompt.kind == "question" && detail != null -> previewItems("prompt-detail", headPreview(detail, PREVIEW_LINES), detail, false, spec, controls)
        prompt.kind != "permission" && prompt.kind != "question" -> prompt.raw_tail?.takeIf { it.isNotBlank() }?.let { tail ->
            previewItems("prompt-tail", tailPreview(tail, PREVIEW_LINES), tail, true, spec, controls)
        }
    }

    if (controls.sent || controls.error != null) {
        item(key = "prompt-feedback") {
            val fb = controls.error
            Text(
                text = fb?.message ?: stringResource(R.string.sent_waiting),
                modifier = transformedItem(spec).padding(vertical = 2.dp),
                color = if (fb != null) Red else Green,
                style = MaterialTheme.typography.labelMedium,
                textAlign = TextAlign.Center
            )
        }
    }

    when (prompt.kind) {
        "permission" -> permissionItems(prompt, spec, controls)
        "question" -> questionItems(prompt, spec, controls)
        else -> unknownItems(spec, controls)
    }
}

private fun TransformingLazyColumnScope.previewItems(
    key: String,
    preview: TextPreview,
    full: String,
    monospace: Boolean,
    spec: TransformationSpec,
    controls: PromptControls
) {
    item(key = key) {
        Text(
            text = preview.text,
            modifier = transformedItem(spec)
                .background(SurfaceLow, RoundedCornerShape(12.dp))
                .padding(horizontal = 10.dp, vertical = 8.dp),
            color = OnSurface,
            style = if (monospace) {
                MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 17.sp)
            } else {
                MaterialTheme.typography.bodyLarge
            }
        )
    }
    if (preview.isTruncated) {
        item(key = "$key-all") {
            OutlinedButton(
                onClick = { controls.onViewAll(full) },
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec),
                label = { Text(stringResource(R.string.view_all)) }
            )
        }
    }
}

private fun TransformingLazyColumnScope.permissionItems(prompt: PendingPrompt, spec: TransformationSpec, controls: PromptControls) {
    val buttons = permissionButtons(prompt)
    item(key = "prompt-primary") {
        // Wear convention: the positive action on the right.
        Row(
            modifier = transformedItem(spec).padding(top = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            ChoiceButton(
                onClick = { buttons.deny?.let(controls.onAnswer) ?: controls.onCancel() },
                enabled = !controls.locked,
                icon = R.drawable.ic_close,
                label = stringResource(R.string.deny),
                container = SurfaceHigh,
                content = Red,
                modifier = Modifier.weight(1f)
            )
            buttons.allow?.let { allow ->
                ChoiceButton(
                    onClick = { controls.onAnswer(allow) },
                    enabled = !controls.locked,
                    icon = R.drawable.ic_check,
                    label = stringResource(R.string.allow),
                    container = Green,
                    content = OnGreen,
                    modifier = Modifier.weight(1f)
                )
            }
        }
    }
    val more = morePermissionOptions(prompt)
    if (more.isNotEmpty()) {
        item(key = "prompt-more") {
            ListSubHeader(
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp).transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec)
            ) { Text(stringResource(R.string.more_options)) }
        }
        more.forEach { option -> optionItem(option, spec, controls, tonal = false) }
    }
}

private fun TransformingLazyColumnScope.questionItems(prompt: PendingPrompt, spec: TransformationSpec, controls: PromptControls) {
    prompt.options.forEach { option -> optionItem(option, spec, controls, tonal = true) }
    cancelItem(spec, controls)
}

private fun TransformingLazyColumnScope.unknownItems(spec: TransformationSpec, controls: PromptControls) {
    item(key = "prompt-mac") {
        Row(transformedItem(spec).padding(vertical = 4.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
            ResIcon(R.drawable.ic_computer, null, OnSurfaceVariant, Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text(stringResource(R.string.answer_on_device), color = OnSurfaceVariant, style = MaterialTheme.typography.labelMedium)
        }
    }
    cancelItem(spec, controls)
}

private fun TransformingLazyColumnScope.optionItem(option: PromptOption, spec: TransformationSpec, controls: PromptControls, tonal: Boolean) {
    item(key = "option-${option.id}") {
        val warn = option.needsConfirmation()
        val icon: (@Composable BoxScope.() -> Unit)? =
            if (warn) ({ ResIcon(R.drawable.ic_status_blocked, null, Amber, Modifier.size(20.dp)) }) else null
        val label: @Composable RowScope.() -> Unit =
            { Text(option.label, maxLines = 2, overflow = TextOverflow.Ellipsis) }
        // The answer's description (Claude and OpenCode questions print one under each option).
        val description: (@Composable RowScope.() -> Unit)? = option.description?.takeIf { it.isNotBlank() }?.let {
            { Text(it, maxLines = 3, overflow = TextOverflow.Ellipsis) }
        }
        val modifier = Modifier.fillMaxWidth().transformedHeight(this, spec)
        if (tonal && !warn) {
            FilledTonalButton(
                onClick = { controls.onAnswer(option) },
                enabled = !controls.locked,
                modifier = modifier,
                transformation = SurfaceTransformation(spec),
                secondaryLabel = description,
                label = label
            )
        } else {
            OutlinedButton(
                onClick = { controls.onAnswer(option) },
                enabled = !controls.locked,
                modifier = modifier,
                transformation = SurfaceTransformation(spec),
                border = if (warn) ButtonDefaults.outlinedButtonBorder(enabled = !controls.locked, borderColor = Amber) else ButtonDefaults.outlinedButtonBorder(enabled = !controls.locked),
                icon = icon,
                secondaryLabel = description,
                label = label
            )
        }
    }
}

private fun TransformingLazyColumnScope.cancelItem(spec: TransformationSpec, controls: PromptControls) {
    item(key = "prompt-cancel") {
        OutlinedButton(
            onClick = controls.onCancel,
            enabled = !controls.locked,
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp).transformedHeight(this, spec),
            transformation = SurfaceTransformation(spec),
            colors = ButtonDefaults.outlinedButtonColors(contentColor = Red, iconColor = Red),
            icon = { ResIcon(R.drawable.ic_close, null, Red, Modifier.size(20.dp)) },
            label = { Text(stringResource(R.string.cancel_prompt)) }
        )
    }
}

/** Half-width answer button: icon above the word, so "Allow" and "Deny" never wrap. */
@Composable
private fun ChoiceButton(
    onClick: () -> Unit,
    enabled: Boolean,
    icon: Int,
    label: String,
    container: Color,
    content: Color,
    modifier: Modifier
) {
    Button(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier,
        colors = ButtonDefaults.buttonColors(containerColor = container, contentColor = content, iconColor = content),
        contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp)
    ) {
        Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            ResIcon(icon, null, content, Modifier.size(20.dp))
            Text(label, fontWeight = FontWeight.SemiBold, maxLines = 1)
        }
    }
}
