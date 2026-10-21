package com.gabriel.agentwatch.mobile.ui

import androidx.compose.foundation.background
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
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

/**
 * One answer, as the blocks `:core` produced ([com.gabriel.agentwatch.util.readerBlocks]): markdown
 * for a transcript, verbatim text for a screen capture. Same information architecture as the watch's
 * reader, with the phone's own typography and layout (wearos-app/ARCHITECTURE.md §4a).
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
        is MdBlock.Code -> Text(
            text = block.text,
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(8.dp))
                .padding(horizontal = 12.dp, vertical = 8.dp),
            style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
        )
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

@Composable
private fun codeSpanStyle(): SpanStyle =
    SpanStyle(fontFamily = FontFamily.Monospace, background = MaterialTheme.colorScheme.surfaceVariant)

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
