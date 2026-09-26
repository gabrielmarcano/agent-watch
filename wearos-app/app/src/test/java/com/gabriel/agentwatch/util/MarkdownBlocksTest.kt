package com.gabriel.agentwatch.util

import com.gabriel.agentwatch.util.MdBlock.Code
import com.gabriel.agentwatch.util.MdBlock.Heading
import com.gabriel.agentwatch.util.MdBlock.ListItem
import com.gabriel.agentwatch.util.MdBlock.Paragraph
import com.gabriel.agentwatch.util.MdBlock.Quote
import com.gabriel.agentwatch.util.MdBlock.Record
import com.gabriel.agentwatch.util.MdBlock.Rule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownBlocksTest {

    private fun plain(text: String) = listOf(MdSpan(text))

    @Test
    fun headingsParagraphsAndSoftWraps() {
        val blocks = parseMarkdown("## Release 2.4\n\nI drafted the notes\nfrom 14 PRs.\n\n### Breaking")
        assertEquals(
            listOf(
                Heading(2, plain("Release 2.4")),
                Paragraph(plain("I drafted the notes from 14 PRs.")),
                Heading(3, plain("Breaking")),
            ),
            blocks
        )
    }

    @Test
    fun everyListItemIsItsOwnBlock() {
        val blocks = parseMarkdown("- Dark mode\n* Faster import\n1. First\n2) Second\n  - nested")
        assertEquals(
            listOf(
                ListItem("•", plain("Dark mode"), 0),
                ListItem("•", plain("Faster import"), 0),
                ListItem("1.", plain("First"), 0),
                ListItem("2.", plain("Second"), 0),
                ListItem("•", plain("nested"), 1),
            ),
            blocks
        )
    }

    @Test
    fun fencedCodeKeepsItsLinesVerbatim() {
        val blocks = parseMarkdown("Run:\n\n```bash\ngit diff --stat\n  1 file changed\n```\nDone.")
        assertEquals(
            listOf(
                Paragraph(plain("Run:")),
                Code("git diff --stat\n  1 file changed"),
                Paragraph(plain("Done.")),
            ),
            blocks
        )
    }

    @Test
    fun anUnclosedFenceRunsToTheEnd() {
        assertEquals(listOf(Code("a\nb")), parseMarkdown("```\na\nb"))
    }

    @Test
    fun eachTableRowBecomesARecordOfHeaderAndValue() {
        // A table does not fit 170 dp: every row is its own item, one "header: value" line per column.
        val blocks = parseMarkdown("| File | Lines | Status |\n|:---|---:|:---:|\n| `app.ts` | 42 | **ok** |\n| auth.ts | 7 | failing |")
        assertEquals(
            listOf(
                Record(listOf("File" to listOf(MdSpan("app.ts", code = true)), "Lines" to plain("42"), "Status" to listOf(MdSpan("ok", bold = true)))),
                Record(listOf("File" to plain("auth.ts"), "Lines" to plain("7"), "Status" to plain("failing"))),
            ),
            blocks
        )
    }

    @Test
    fun missingCellsAreLeftOutAndExtraCellsKeepAPlainHeader() {
        val blocks = parseMarkdown("| a | b |\n|---|---|\n| 1 |\n| 1 | 2 | 3 |")
        assertEquals(
            listOf(
                Record(listOf("a" to plain("1"))),
                Record(listOf("a" to plain("1"), "b" to plain("2"), "" to plain("3"))),
            ),
            blocks
        )
    }

    @Test
    fun pipeLinesWithoutASeparatorStayVerbatim() {
        assertEquals(listOf(Code("| not | a table |")), parseMarkdown("| not | a table |"))
    }

    @Test
    fun quotesAndRules() {
        assertEquals(
            listOf(Quote(plain("Careful here")), Rule),
            parseMarkdown("> Careful\n> here\n\n---")
        )
    }

    @Test
    fun inlineBoldItalicCodeAndLinks() {
        val spans = parseInline("Updated **14 PRs** in `CHANGELOG.md`, see [the PR](https://x.y/1) and *soon*.")
        assertEquals(
            listOf(
                MdSpan("Updated "),
                MdSpan("14 PRs", bold = true),
                MdSpan(" in "),
                MdSpan("CHANGELOG.md", code = true),
                MdSpan(", see the PR and "), // same-style neighbours are merged; a link keeps only its text
                MdSpan("soon", italic = true),
                MdSpan("."),
            ),
            spans
        )
    }

    @Test
    fun snakeCaseIsNotItalic() {
        assertEquals(plain("set AW_PUSH_RESOLVED and my_var"), parseInline("set AW_PUSH_RESOLVED and my_var"))
    }

    @Test
    fun anUnmatchedMarkerStaysLiteral() {
        assertEquals(plain("2 * 3 = 6 and **open"), parseInline("2 * 3 = 6 and **open"))
    }

    @Test
    fun codeSpansAreNotParsedInside() {
        assertEquals(listOf(MdSpan("**x**", code = true)), parseInline("`**x**`"))
    }

    @Test
    fun blankInputHasNoBlocks() {
        assertTrue(parseMarkdown("  \n\n").isEmpty())
    }

    @Test
    fun screenTextSplitsOnBlankLinesAndStaysVerbatim() {
        val blocks = screenBlocks("✦ Done.\n  ran 3 migrations\n\n\n  > Type your message\n")
        assertEquals(listOf(Code("✦ Done.\n  ran 3 migrations"), Code("  > Type your message")), blocks)
    }
}
