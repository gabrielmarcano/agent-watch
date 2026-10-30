package com.gabriel.agentwatch.ui.logic

import android.app.Activity
import com.gabriel.agentwatch.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class InputResultTest {

    private val ok = Activity.RESULT_OK
    private val canceled = Activity.RESULT_CANCELED

    @Test
    fun remoteInputTextIsRead() {
        assertEquals(InputResult.Text("run the tests"), inputResult(ok, "run the tests", null, null))
    }

    @Test
    fun remoteInputWinsOverTheOtherForms() {
        assertEquals(InputResult.Text("typed"), inputResult(ok, "typed", listOf("spoken"), "extra"))
    }

    // The recognizer's form, kept as a fallback.
    @Test
    fun recognizerResultsAreAFallback() {
        assertEquals(InputResult.Text("spoken"), inputResult(ok, null, listOf("spoken", "spoke in"), null))
        assertEquals(InputResult.Text("second"), inputResult(ok, "  ", listOf("", null, "second"), null))
    }

    @Test
    fun extraTextIsTheLastFallback() {
        assertEquals(InputResult.Text("plain"), inputResult(ok, null, emptyList(), "plain"))
    }

    @Test
    fun textIsTrimmed() {
        assertEquals(InputResult.Text("hello"), inputResult(ok, "  hello \n", null, null))
    }

    // Text is never dropped, even with an odd result code: it only reaches the confirm screen.
    @Test
    fun textCountsWhateverTheResultCode() {
        assertEquals(InputResult.Text("kept"), inputResult(canceled, "kept", null, null))
        assertEquals(InputResult.Text("kept"), inputResult(42, null, listOf("kept"), null))
    }

    @Test
    fun okWithoutTextIsEmpty() {
        assertEquals(InputResult.Empty, inputResult(ok, null, null, null))
        assertEquals(InputResult.Empty, inputResult(ok, " ", listOf(" "), "\n"))
    }

    @Test
    fun backingOutIsCanceled() {
        assertEquals(InputResult.Canceled, inputResult(canceled, null, null, null))
    }

    @Test
    fun anyOtherCodeIsUnexpected() {
        assertEquals(InputResult.Unexpected(42), inputResult(42, null, null, null))
    }

    // Only a back-out is silent: every other result without text shows a line.
    @Test
    fun feedbackForEveryResultWithoutText() {
        assertNull(inputFeedback(InputResult.Text("x")))
        assertNull(inputFeedback(InputResult.Canceled))
        assertEquals(R.string.input_empty, inputFeedback(InputResult.Empty))
        assertEquals(R.string.input_failed, inputFeedback(InputResult.Unexpected(42)))
    }
}
