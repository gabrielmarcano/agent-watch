package com.gabriel.agentwatch.ui.logic

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TextPreviewTest {

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
