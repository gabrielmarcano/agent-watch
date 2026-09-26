package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.background
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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.foundation.lazy.itemsIndexed
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.Text
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.model.HistoryItem
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.ageText
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.theme.Blue
import com.gabriel.agentwatch.ui.theme.OnSurface
import com.gabriel.agentwatch.ui.theme.OnSurfaceVariant
import com.gabriel.agentwatch.ui.theme.OutlineVariant
import com.gabriel.agentwatch.ui.theme.SurfaceLow
import com.gabriel.agentwatch.util.MdBlock
import com.gabriel.agentwatch.util.MdSpan
import com.gabriel.agentwatch.util.parseMarkdown
import com.gabriel.agentwatch.util.screenBlocks

/**
 * One finished turn. A transcript answer is markdown, split into blocks so each is its own list item
 * and the round edges never clip a long answer; a `screen` capture is verbatim monospace.
 */
@Composable
fun ResponseReaderScreen(item: HistoryItem) {
    val blocks = remember(item.response, item.source) {
        if (item.source == "transcript") parseMarkdown(item.response) else screenBlocks(item.response)
    }
    ScreenList { spec ->
        item(key = "header") {
            Column(transformedItem(spec).padding(start = 18.dp, end = 18.dp, top = 6.dp, bottom = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                Text(
                    item.label.ifBlank { item.agent },
                    style = MaterialTheme.typography.titleMedium,
                    textAlign = TextAlign.Center,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis
                )
                val meta = listOfNotNull(
                    item.agent.takeIf { it.isNotBlank() },
                    ageText(item.completed_at).takeIf { it.isNotBlank() },
                    if (item.source == "screen") stringResource(R.string.reader_screen_source) else null
                ).joinToString(" · ")
                Text(meta, color = OnSurfaceVariant, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
            }
        }
        item.query?.takeIf { it.isNotBlank() }?.let { query ->
            item(key = "query") {
                Column(
                    transformedItem(spec)
                        .background(SurfaceLow, RoundedCornerShape(12.dp))
                        .padding(horizontal = 12.dp, vertical = 8.dp)
                ) {
                    Text(stringResource(R.string.you_asked), color = Blue, style = MaterialTheme.typography.labelSmall)
                    Text(query, color = OnSurface, style = MaterialTheme.typography.bodyMedium)
                }
            }
        }
        itemsIndexed(blocks) { _, block -> MarkdownBlockView(block, transformedItem(spec)) }
    }
}

private val codeStyle = SpanStyle(fontFamily = FontFamily.Monospace, background = SurfaceLow)

private fun spansToAnnotated(spans: List<MdSpan>): AnnotatedString = buildAnnotatedString {
    spans.forEach { span ->
        val style = SpanStyle(
            fontWeight = if (span.bold) FontWeight.Bold else null,
            fontStyle = if (span.italic) FontStyle.Italic else null
        ).let { if (span.code) it.merge(codeStyle) else it }
        withStyle(style) { append(span.text) }
    }
}

@Composable
private fun MarkdownBlockView(block: MdBlock, modifier: Modifier) {
    val body = MaterialTheme.typography.bodyMedium
    when (block) {
        is MdBlock.Heading -> Text(
            spansToAnnotated(block.spans),
            modifier = modifier.padding(top = 6.dp),
            style = if (block.level <= 2) MaterialTheme.typography.titleMedium else MaterialTheme.typography.titleSmall,
            color = OnSurface
        )
        is MdBlock.Paragraph -> Text(spansToAnnotated(block.spans), modifier = modifier, style = body, color = OnSurface)
        is MdBlock.ListItem -> Row(modifier.padding(start = (block.depth * 10).dp)) {
            Text(block.marker, style = body, color = OnSurfaceVariant)
            Spacer(Modifier.width(6.dp))
            Text(spansToAnnotated(block.spans), style = body, color = OnSurface)
        }
        is MdBlock.Code -> Text(
            block.text,
            modifier = modifier
                .background(SurfaceLow, RoundedCornerShape(8.dp))
                .padding(horizontal = 10.dp, vertical = 6.dp),
            style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 17.sp),
            color = OnSurface
        )
        is MdBlock.Quote -> Row(modifier.height(IntrinsicSize.Min)) {
            Box(Modifier.width(3.dp).fillMaxHeight().background(OutlineVariant))
            Spacer(Modifier.width(8.dp))
            Text(spansToAnnotated(block.spans), style = body, color = OnSurfaceVariant)
        }
        MdBlock.Rule -> Box(modifier.padding(vertical = 6.dp).height(1.dp).background(OutlineVariant))
    }
}
