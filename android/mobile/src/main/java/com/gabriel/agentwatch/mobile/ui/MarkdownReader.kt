package com.gabriel.agentwatch.mobile.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.gabriel.agentwatch.mobile.R
import com.gabriel.agentwatch.util.MdBlock
import com.gabriel.agentwatch.util.MdSpan
import kotlinx.coroutines.delay

/**
 * One answer, as the blocks `:core` produced ([com.gabriel.agentwatch.util.readerBlocks]): markdown
 * for a transcript, verbatim text for a screen capture. Same information architecture as the watch's
 * reader, with the phone's own typography and layout (android/ARCHITECTURE.md §4a).
 */
@Composable
fun MarkdownBlocks(blocks: List<MdBlock>, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        blocks.forEach { MarkdownBlock(it) }
    }
}

/** The prompt that produced the answer, shown above it like the watch's "You asked" box. */
@Composable
fun AskedBox(query: String) {
    Column(
        Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(12.dp))
            .padding(horizontal = 12.dp, vertical = 8.dp)
    ) {
        Text(stringResource(R.string.you_asked), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelSmall)
        Text(query, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun MarkdownBlock(block: MdBlock) {
    val body = MaterialTheme.typography.bodyLarge
    when (block) {
        is MdBlock.Heading -> Text(
            spansToAnnotated(block.spans),
            style = if (block.level <= 2) MaterialTheme.typography.titleLarge else MaterialTheme.typography.titleMedium
        )
        is MdBlock.Paragraph -> Text(spansToAnnotated(block.spans), style = body)
        is MdBlock.ListItem -> Row(Modifier.padding(start = (block.depth * 16).dp)) {
            Text(block.marker, style = body, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.width(8.dp))
            Text(spansToAnnotated(block.spans), style = body)
        }
        is MdBlock.Code -> CodeBlock(block.text)
        is MdBlock.Quote -> Row(Modifier.height(IntrinsicSize.Min)) {
            Box(Modifier.width(3.dp).fillMaxHeight().background(MaterialTheme.colorScheme.outlineVariant))
            Spacer(Modifier.width(10.dp))
            Text(spansToAnnotated(block.spans), style = body, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        MdBlock.Rule -> Box(Modifier.fillMaxWidth().padding(vertical = 4.dp).height(1.dp).background(MaterialTheme.colorScheme.outlineVariant))
        // A table row: the first cell as the title, then one "header  value" line per column.
        is MdBlock.Record -> Column(
            Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(12.dp))
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp)
        ) {
            block.fields.forEachIndexed { index, (header, value) ->
                if (index == 0) {
                    Text(spansToAnnotated(value), style = MaterialTheme.typography.titleSmall)
                } else {
                    Text(
                        buildAnnotatedString {
                            if (header.isNotBlank()) {
                                withStyle(SpanStyle(color = MaterialTheme.colorScheme.onSurfaceVariant)) { append(header) }
                                append("  ")
                            }
                            append(spansToAnnotated(value))
                        },
                        style = body
                    )
                }
            }
        }
    }
}

/** A fenced code block: horizontal scroll so lines never wrap, and a one-tap copy button. */
@Composable
private fun CodeBlock(text: String) {
    val context = LocalContext.current
    val haptics = LocalHapticFeedback.current
    var copied by remember(text) { mutableStateOf(false) }

    LaunchedEffect(copied) {
        if (copied) {
            delay(1800)
            copied = false
        }
    }

    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .background(MaterialTheme.colorScheme.surfaceContainerHigh)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.surfaceContainerHighest.copy(alpha = 0.6f))
                .clickable {
                    val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
                    cm?.setPrimaryClip(ClipData.newPlainText("code", text))
                    haptics.performHapticFeedback(HapticFeedbackType.KeyboardTap)
                    copied = true
                }
                .padding(horizontal = 12.dp, vertical = 5.dp),
            horizontalArrangement = Arrangement.End,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                imageVector = if (copied) Icons.Filled.Check else Icons.Filled.ContentCopy,
                contentDescription = null,
                modifier = Modifier.size(13.dp),
                tint = if (copied) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
            )
            Spacer(Modifier.width(5.dp))
            Text(
                text = stringResource(if (copied) R.string.chat_copied else R.string.chat_copy),
                style = MaterialTheme.typography.labelSmall,
                color = if (copied) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Box(
            Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 12.dp, vertical = 8.dp)
        ) {
            Text(
                text = text,
                style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace)
            )
        }
    }
}

@Composable
private fun codeSpanStyle(): SpanStyle = SpanStyle(fontFamily = FontFamily.Monospace)

@Composable
private fun spansToAnnotated(spans: List<MdSpan>): AnnotatedString = buildAnnotatedString {
    val code = codeSpanStyle()
    spans.forEach { span ->
        val style = SpanStyle(
            fontWeight = if (span.bold) FontWeight.Bold else null,
            fontStyle = if (span.italic) FontStyle.Italic else null
        ).let { if (span.code) it.merge(code) else it }
        withStyle(style) { append(span.text) }
    }
}
