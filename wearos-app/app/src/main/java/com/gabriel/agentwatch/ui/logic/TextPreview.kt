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

/**
 * A long reply's start and end, joined by a "…" line: its first line usually holds the conclusion
 * and its end the question or the next step; the middle is detail, left to the reader. The head is
 * the first line cut to [headChars]; the tail is the last lines that fit in [tailChars] (the last
 * line's end, cut at a word, when it alone is longer). Null when the whole text fits.
 */
fun headTailPreview(text: String, headChars: Int, tailChars: Int): String? {
    val all = lines(text).filter { it.isNotBlank() }
    if (all.isEmpty() || all.sumOf { it.length + 1 } - 1 <= headChars + tailChars) return null
    val head = cutEnd(all.first(), headChars)
    val tail = ArrayDeque<String>()
    var used = 0
    for (line in all.drop(1).asReversed()) {
        val cost = line.length + if (tail.isEmpty()) 0 else 1
        if (used + cost > tailChars) break
        tail.addFirst(line)
        used += cost
    }
    if (tail.isEmpty()) tail.addFirst(cutStart(all.last(), tailChars))
    return head + "\n…\n" + tail.joinToString("\n")
}

/** [s] cut to [max] chars at a word, with "…" at the end. */
private fun cutEnd(s: String, max: Int): String {
    if (s.length <= max) return s
    val cut = s.take(max - 1)
    val space = cut.lastIndexOf(' ')
    return (if (space > max / 2) cut.take(space) else cut).trimEnd() + "…"
}

/** The end of [s] in [max] chars, starting at a word, with "…" in front. */
private fun cutStart(s: String, max: Int): String {
    if (s.length <= max) return s
    val cut = s.takeLast(max - 1)
    val space = cut.indexOf(' ')
    return "…" + (if (space in 0 until max / 2) cut.drop(space + 1) else cut).trimStart()
}
