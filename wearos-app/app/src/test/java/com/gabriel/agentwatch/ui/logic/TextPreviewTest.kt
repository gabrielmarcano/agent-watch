package com.gabriel.agentwatch.ui.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TextPreviewTest {

    // A long reply shows its first line (the conclusion) and its end (the
    // question or the next step); the middle is left out.
    @Test
    fun headTailKeepsTheFirstLineAndTheEnd() {
        val reply = listOf(
            "The app 1.2.0 is installed",
            "Details follow."
        ) + (1..10).map { "a detail line that only matters in the reader" } + listOf(
            "No push yet: 4 local commits.",
            "Shall I push?"
        )
        val preview = headTailPreview(reply.joinToString("\n"), headChars = 70, tailChars = 60)
        assertEquals("The app 1.2.0 is installed\n…\nNo push yet: 4 local commits.\nShall I push?", preview)
    }

    @Test
    fun headTailIsNullWhenTheTextFits() {
        assertEquals(null, headTailPreview("Done.\nShall I push?", headChars = 70, tailChars = 100))
    }

    @Test
    fun headTailCutsAnOverlongFirstLineAndLastLineAtWords() {
        val first = "word ".repeat(30).trim()
        val last = "end ".repeat(40).trim() + " question?"
        val preview = headTailPreview(first + "\nmiddle\n" + last, headChars = 40, tailChars = 50)!!
        val (head, cut, tail) = preview.split("\n")
        assertTrue(head.length <= 40 && head.endsWith("…"))
        assertEquals("…", cut)
        assertTrue(tail.length <= 50 && tail.startsWith("…") && tail.endsWith("question?"))
        assertFalse(tail.startsWith("…n") || tail.startsWith("…d")) // starts at a word
    }

    private val twelve = (1..12).joinToString("\n") { "line $it" }

    @Test
    fun tailKeepsTheLastLinesWhereTheQuestionIs() {
        val preview = tailPreview(twelve, maxLines = 6)
        assertEquals((7..12).joinToString("\n") { "line $it" }, preview.text)
        assertEquals(6, preview.hiddenLines)
        assertTrue(preview.isTruncated)
    }

    @Test
    fun headKeepsTheFirstLinesOfACommand() {
        val preview = headPreview(twelve, maxLines = 6)
        assertEquals((1..6).joinToString("\n") { "line $it" }, preview.text)
        assertEquals(6, preview.hiddenLines)
    }

    @Test
    fun shortTextIsNotTruncated() {
        val preview = headPreview("go test ./...", maxLines = 6)
        assertEquals("go test ./...", preview.text)
        assertEquals(0, preview.hiddenLines)
        assertFalse(preview.isTruncated)
        assertFalse(tailPreview("a\nb", maxLines = 6).isTruncated)
    }

    @Test
    fun trailingBlankLinesDoNotCountAndDoNotShow() {
        val preview = tailPreview("a\nb\nc\n\n  \n", maxLines = 2)
        assertEquals("b\nc", preview.text)
        assertEquals(1, preview.hiddenLines)
    }

    @Test
    fun carriageReturnsAreNormalised() {
        assertEquals("a\nb", headPreview("a\r\nb\r\n", maxLines = 6).text)
    }
}
