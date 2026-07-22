package com.gabriel.agentwatch.util

object MarkdownFormatter {

    /**
     * Cleans raw markdown syntax from text to render clean, professional typography
     * on small Wear OS watch displays without syntax clutter like '**', '###', etc.
     */
    fun clean(input: String?): String {
        if (input.isNullOrBlank()) return ""

        return input
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
