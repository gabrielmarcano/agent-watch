package com.gabriel.agentwatch.ui.screens

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.dp

@Composable
fun MicrophoneIcon(
    modifier: Modifier = Modifier.size(18.dp),
    color: Color = Color.White
) {
    Canvas(modifier = modifier) {
        val width = size.width
        val height = size.height
        val strokeWidth = 2.dp.toPx()

        // Capsule Body
        val capsuleWidth = width * 0.38f
        val capsuleHeight = height * 0.50f
        val capsuleLeft = (width - capsuleWidth) / 2f
        val capsuleTop = height * 0.08f
        drawRoundRect(
            color = color,
            topLeft = Offset(capsuleLeft, capsuleTop),
            size = Size(capsuleWidth, capsuleHeight),
            cornerRadius = CornerRadius(capsuleWidth / 2f)
        )

        // U-shaped Holder Arc
        val arcTop = height * 0.28f
        val arcHeight = height * 0.40f
        drawArc(
            color = color,
            startAngle = 0f,
            sweepAngle = 180f,
            useCenter = false,
            topLeft = Offset(width * 0.18f, arcTop),
            size = Size(width * 0.64f, arcHeight),
            style = Stroke(width = strokeWidth)
        )

        // Vertical Stem
        val stemTop = arcTop + arcHeight
        val stemBottom = height * 0.88f
        drawLine(
            color = color,
            start = Offset(width / 2f, stemTop),
            end = Offset(width / 2f, stemBottom),
            strokeWidth = strokeWidth
        )

        // Base Line
        val baseWidth = width * 0.44f
        drawLine(
            color = color,
            start = Offset((width - baseWidth) / 2f, stemBottom),
            end = Offset((width + baseWidth) / 2f, stemBottom),
            strokeWidth = strokeWidth
        )
    }
}
