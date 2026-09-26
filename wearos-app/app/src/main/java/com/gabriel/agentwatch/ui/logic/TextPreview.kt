package com.gabriel.agentwatch.ui.logic

/** A cut of a multi-line text for the wrist, and how many lines were left out ("View all"). */
data class TextPreview(val text: String, val hiddenLines: Int) {
    val isTruncated: Boolean get() = hiddenLines > 0
}

private fun lines(text: String): List<String> =
    text.replace("\r\n", "\n").split('\n').dropLastWhile { it.isBlank() }

/** The first [maxLines] lines: for a command, whose start says what it does. */
fun headPreview(text: String, maxLines: Int): TextPreview {
    val all = lines(text)
    return TextPreview(all.take(maxLines).joinToString("\n"), (all.size - maxLines).coerceAtLeast(0))
}

/** The last [maxLines] lines: for a raw screen tail (`unknown` prompts), where the question is at the end. */
fun tailPreview(text: String, maxLines: Int): TextPreview {
    val all = lines(text)
    return TextPreview(all.takeLast(maxLines).joinToString("\n"), (all.size - maxLines).coerceAtLeast(0))
}
