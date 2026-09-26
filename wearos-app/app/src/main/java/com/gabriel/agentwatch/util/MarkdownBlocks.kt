package com.gabriel.agentwatch.util

/** A run of inline text with one style. */
data class MdSpan(
    val text: String,
    val bold: Boolean = false,
    val italic: Boolean = false,
    val code: Boolean = false
)

/**
 * One block of an agent's markdown answer. The reader shows each block as its own list item, so a
 * long answer never sits in one item that the round screen clips.
 */
sealed interface MdBlock {
    data class Heading(val level: Int, val spans: List<MdSpan>) : MdBlock
    data class Paragraph(val spans: List<MdSpan>) : MdBlock
    /** [marker] is "•" or "3."; [depth] 0 is top level. */
    data class ListItem(val marker: String, val spans: List<MdSpan>, val depth: Int) : MdBlock
    /** Verbatim monospace text: fenced code, pipe lines that are not a table, or a terminal screen. */
    data class Code(val text: String) : MdBlock
    /** One table row, as (column header, cell) pairs: a table does not fit a watch, a record does. */
    data class Record(val fields: List<Pair<String, List<MdSpan>>>) : MdBlock
    data class Quote(val spans: List<MdSpan>) : MdBlock
    data object Rule : MdBlock
}

private val HEADING = Regex("^(#{1,6})\\s+(.*?)\\s*#*\\s*$")
private val BULLET = Regex("^(\\s*)[-*+]\\s+(.*)$")
private val NUMBERED = Regex("^(\\s*)(\\d+)[.)]\\s+(.*)$")
private val RULE = Regex("^\\s{0,3}([-*_])(\\s*\\1){2,}\\s*$")
private val FENCE = Regex("^\\s{0,3}(```|~~~)")

/** Splits an agent's markdown answer into blocks. Covers what agents write; anything else is a paragraph. */
fun parseMarkdown(source: String): List<MdBlock> {
    val lines = source.replace("\r\n", "\n").split('\n')
    val blocks = mutableListOf<MdBlock>()
    val paragraph = mutableListOf<String>()
    val quote = mutableListOf<String>()

    fun flush() {
        if (paragraph.isNotEmpty()) blocks += MdBlock.Paragraph(parseInline(paragraph.joinToString(" ")))
        if (quote.isNotEmpty()) blocks += MdBlock.Quote(parseInline(quote.joinToString(" ")))
        paragraph.clear()
        quote.clear()
    }

    var i = 0
    while (i < lines.size) {
        val line = lines[i]
        val trimmed = line.trim()
        when {
            FENCE.containsMatchIn(line) -> {
                flush()
                val fence = FENCE.find(line)!!.groupValues[1]
                val code = mutableListOf<String>()
                i++
                while (i < lines.size && !lines[i].trimStart().startsWith(fence)) code += lines[i++]
                blocks += MdBlock.Code(code.joinToString("\n").trimEnd())
            }
            trimmed.isEmpty() -> flush()
            trimmed.startsWith("|") -> {
                flush()
                val table = mutableListOf<String>()
                while (i < lines.size && lines[i].trim().startsWith("|")) table += lines[i++].trim()
                blocks += tableBlocks(table)
                continue
            }
            HEADING.matches(trimmed) -> {
                flush()
                val m = HEADING.find(trimmed)!!
                blocks += MdBlock.Heading(m.groupValues[1].length, parseInline(m.groupValues[2]))
            }
            RULE.matches(line) -> {
                flush()
                blocks += MdBlock.Rule
            }
            trimmed.startsWith(">") -> {
                if (paragraph.isNotEmpty()) flush()
                quote += trimmed.removePrefix(">").trim()
            }
            BULLET.matches(line) -> {
                flush()
                val m = BULLET.find(line)!!
                blocks += MdBlock.ListItem("•", parseInline(m.groupValues[2]), depthOf(m.groupValues[1]))
            }
            NUMBERED.matches(line) -> {
                flush()
                val m = NUMBERED.find(line)!!
                blocks += MdBlock.ListItem("${m.groupValues[2]}.", parseInline(m.groupValues[3]), depthOf(m.groupValues[1]))
            }
            else -> {
                if (quote.isNotEmpty()) flush()
                paragraph += trimmed
            }
        }
        i++
    }
    flush()
    return blocks
}

private val TABLE_SEPARATOR = Regex("^\\|?\\s*:?-{2,}:?\\s*(\\|\\s*:?-{2,}:?\\s*)*\\|?$")

private fun cells(line: String): List<String> =
    line.trim().removePrefix("|").removeSuffix("|").split('|').map { it.trim() }

/** A markdown table → one [MdBlock.Record] per row; pipe lines without a header separator stay verbatim. */
private fun tableBlocks(lines: List<String>): List<MdBlock> {
    if (lines.size < 2 || !TABLE_SEPARATOR.matches(lines[1])) return listOf(MdBlock.Code(lines.joinToString("\n")))
    val headers = cells(lines[0])
    return lines.drop(2).map { row ->
        MdBlock.Record(
            cells(row).mapIndexedNotNull { col, cell ->
                if (cell.isEmpty()) null else headers.getOrElse(col) { "" } to parseInline(cell)
            }
        )
    }
}

private fun depthOf(indent: String): Int = indent.replace("\t", "    ").length / 2

/**
 * A terminal screen (`source: "screen"`), which is not markdown: verbatim monospace blocks split on
 * blank lines.
 */
fun screenBlocks(screen: String): List<MdBlock> =
    screen.replace("\r\n", "\n")
        .split(Regex("\n\\s*\n"))
        .map { it.trimEnd() }
        .filter { it.isNotBlank() }
        .map { MdBlock.Code(it.trim('\n')) }

/**
 * Inline markdown: `code`, **bold** / __bold__, *italic* / _italic_, [text](url) (the text only),
 * ~~strike~~ (plain). An unmatched marker stays literal; `_` inside a word (snake_case) is not italic.
 * Neighbouring spans with the same style are merged.
 */
fun parseInline(text: String): List<MdSpan> {
    val out = mutableListOf<MdSpan>()
    fun add(s: String, bold: Boolean = false, italic: Boolean = false, code: Boolean = false) {
        if (s.isEmpty()) return
        val last = out.lastOrNull()
        if (last != null && last.bold == bold && last.italic == italic && last.code == code) {
            out[out.lastIndex] = last.copy(text = last.text + s)
        } else {
            out += MdSpan(s, bold, italic, code)
        }
    }

    fun parse(s: String, bold: Boolean, italic: Boolean) {
        var i = 0
        val plain = StringBuilder()
        fun emitPlain() { add(plain.toString(), bold, italic); plain.clear() }
        while (i < s.length) {
            val c = s[i]
            // `code`
            if (c == '`') {
                val end = s.indexOf('`', i + 1)
                if (end > i) {
                    emitPlain(); add(s.substring(i + 1, end), code = true); i = end + 1; continue
                }
            }
            // [text](url)
            if (c == '[') {
                val close = s.indexOf("](", i + 1)
                val paren = if (close > i) s.indexOf(')', close + 2) else -1
                if (close > i && paren > close) {
                    emitPlain(); parse(s.substring(i + 1, close), bold, italic); i = paren + 1; continue
                }
            }
            // **bold**, __bold__, ~~strike~~
            if ((c == '*' || c == '_' || c == '~') && s.startsWith("$c$c", i)) {
                val marker = "$c$c"
                val end = s.indexOf(marker, i + 2)
                if (end > i + 2 && (c != '_' || isBoundary(s, i - 1)) ) {
                    emitPlain()
                    parse(s.substring(i + 2, end), bold || c != '~', italic)
                    i = end + 2; continue
                }
            }
            // *italic*, _italic_ (a `_` must open and close at word boundaries)
            if ((c == '*' || c == '_') && i + 1 < s.length && !s[i + 1].isWhitespace() &&
                (c == '*' || isBoundary(s, i - 1))
            ) {
                var end = s.indexOf(c, i + 1)
                while (end > 0 && (s[end - 1].isWhitespace() || (c == '_' && !isBoundary(s, end + 1)))) {
                    end = s.indexOf(c, end + 1)
                }
                if (end > i + 1) {
                    emitPlain(); parse(s.substring(i + 1, end), bold, true); i = end + 1; continue
                }
            }
            plain.append(c)
            i++
        }
        emitPlain()
    }

    parse(text, bold = false, italic = false)
    return out
}

private fun isBoundary(s: String, index: Int): Boolean = index !in s.indices || !s[index].isLetterOrDigit()
