package ru.rassvet.chat

import java.security.SecureRandom
import java.util.UUID

internal object Uuid7 {
    private val random = SecureRandom()

    fun new(): String {
        val time = System.currentTimeMillis() and 0xFFFFFFFFFFFFL
        val high = (time shl 16) or 0x7000L or random.nextInt(4096).toLong()
        val low = (random.nextLong() and 0x3FFFFFFFFFFFFFFFL) or Long.MIN_VALUE
        return UUID(high, low).toString()
    }
}

internal fun shortId(id: String): String =
    if (id.length == 36) "${id.take(8)}…${id.takeLast(4)}" else id
