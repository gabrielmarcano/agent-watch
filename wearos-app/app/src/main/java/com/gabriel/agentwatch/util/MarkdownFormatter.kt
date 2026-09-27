package com.gabriel.agentwatch.util

object MarkdownFormatter {

    /**
     * Cleans raw markdown syntax from text to render clean, professional typography
     * on small Wear OS watch displays without syntax clutter like '**', '###', etc.
     */
    fun clean(input: String?): String {
        if (input.isNullOrBlank()) return ""

        return tableRows(input)
            // Remove code block markers but keep code content formatted
            .replace(Regex("```(?:[a-zA-Z]*)\\n?"), "")
            .replace(Regex("```"), "")
            // Bold & Italics
            .replace(Regex("\\*\\*([^*]+)\\*\\*"), "$1")
            .replace(Regex("__([^_]+)__"), "$1")
            .replace(Regex("\\*([^*]+)\\*"), "$1")
            .replace(Regex("_([^_]+)_"), "$1")
            // Inline code backticks
            .replace(Regex("`([^`]+)`"), "$1")
            // Headers (### Header -> Header)
            .replace(Regex("(?m)^#{1,6}\\s*"), "")
            // Unordered list items (- or * or + -> •)
            .replace(Regex("(?m)^\\s*[-*+]\\s+"), "• ")
            // Markdown links ([title](url) -> title)
            .replace(Regex("\\[([^\\]]+)\\]\\([^\\)]+\\)"), "$1")
            // Remove HTML tags if any
            .replace(Regex("<[^>]*>"), "")
            // Reduce multiple consecutive blank lines to a single blank line
            .replace(Regex("\\n{3,}"), "\n\n")
            .trim()
    }

    /**
     * A markdown table as one line per row, "first cell: other cells · …", without its header and
     * separator: a grid of pipes does not fit a preview.
     */
    private fun tableRows(input: String): String {
        val lines = input.replace("\r\n", "\n").split('\n')
        val out = mutableListOf<String>()
        var i = 0
        while (i < lines.size) {
            if (!isTableStart(lines, i)) {
                out += lines[i++]
                continue
            }
            i += 2
            while (i < lines.size && lines[i].trim().startsWith("|")) {
                val cells = tableCells(lines[i++]).filter { it.isNotEmpty() }
                if (cells.isNotEmpty()) out += cells.first() + cells.drop(1).joinToString(" · ", prefix = if (cells.size > 1) ": " else "")
            }
        }
        return out.joinToString("\n")
    }

    /**
     * Truncates text cleanly at word boundaries up to maxChars
     */
    fun truncate(input: String?, maxChars: Int = 140): String {
        val cleaned = clean(input)
        if (cleaned.length <= maxChars) return cleaned
        
        val truncated = cleaned.take(maxChars)
        val lastSpace = truncated.lastIndexOf(' ')
        return if (lastSpace > maxChars / 2) {
            truncated.substring(0, lastSpace) + "…"
        } else {
            truncated + "…"
        }
    }
}
