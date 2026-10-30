package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.wear.compose.foundation.lazy.items
import androidx.wear.compose.material3.ListHeader
import androidx.wear.compose.material3.MaterialTheme
import androidx.wear.compose.material3.SurfaceTransformation
import androidx.wear.compose.material3.Text
import androidx.wear.compose.material3.lazy.transformedHeight
import com.gabriel.agentwatch.R
import com.gabriel.agentwatch.ui.components.ScreenList
import com.gabriel.agentwatch.ui.components.transformedItem
import com.gabriel.agentwatch.ui.theme.OnSurface
import com.gabriel.agentwatch.ui.theme.SurfaceLow

private const val LINES_PER_ITEM = 4

/** A command or screen tail in full ("View all"), in short monospace chunks so the round edges never clip a long block. */
@Composable
fun FullTextScreen(text: String) {
    val chunks = remember(text) {
        text.replace("\r\n", "\n").trimEnd().split('\n').chunked(LINES_PER_ITEM).map { it.joinToString("\n") }
    }
    ScreenList { spec ->
        item(key = "title") {
            ListHeader(
                modifier = Modifier.fillMaxWidth().transformedHeight(this, spec),
                transformation = SurfaceTransformation(spec)
            ) { Text(stringResource(R.string.view_all_title)) }
        }
        items(chunks) { chunk ->
            Text(
                chunk,
                modifier = transformedItem(spec)
                    .background(SurfaceLow, RoundedCornerShape(8.dp))
                    .padding(horizontal = 10.dp, vertical = 6.dp),
                color = OnSurface,
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 17.sp)
            )
        }
    }
}
