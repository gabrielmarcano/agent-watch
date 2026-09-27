package com.gabriel.agentwatch.util

import org.junit.Assert.assertEquals
import org.junit.Test

class MarkdownFormatterTest {

    @Test
    fun aTableReadsAsOneLinePerRowWithoutItsHeader() {
        val reply = "Two lanes:\n\n| Lane | Tasks | Hours |\n|---|:---:|---|\n| **API** | CRUD, errors | 20 |\n| a\\|b | | 3 |\n\nDatabase first."
        assertEquals("Two lanes:\n\nAPI: CRUD, errors · 20\na|b: 3\n\nDatabase first.", MarkdownFormatter.clean(reply))
    }

    @Test
    fun pipeLinesWithoutASeparatorAreNotATable() {
        assertEquals("| not | a table |", MarkdownFormatter.clean("| not | a table |"))
    }
}
