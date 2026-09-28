package ru.rassvet.chat

import android.content.Context
import android.util.Base64
import java.security.MessageDigest
import java.security.SecureRandom
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.PBEKeySpec

internal class PinLock(context: Context) {
    private val preferences = context.getSharedPreferences("pin", Context.MODE_PRIVATE)

    fun isSet(): Boolean = preferences.contains("hash") && preferences.contains("salt")

    fun set(pin: String) {
        require(pin.length in 4..12 && pin.all { it.isDigit() }) { "PIN должен содержать от 4 до 12 цифр" }
        val salt = ByteArray(16).also { SecureRandom().nextBytes(it) }
        val hash = derive(pin, salt)
        val saved = preferences.edit()
            .putString("salt", Base64.encodeToString(salt, Base64.NO_WRAP))
            .putString("hash", Base64.encodeToString(hash, Base64.NO_WRAP))
            .commit()
        check(saved) { "Не удалось сохранить PIN" }
    }

    fun matches(pin: String): Boolean {
        if (!isSet() || pin.length !in 4..12 || pin.any { !it.isDigit() }) return false
        val salt = Base64.decode(preferences.getString("salt", ""), Base64.DEFAULT)
        val expected = Base64.decode(preferences.getString("hash", ""), Base64.DEFAULT)
        return MessageDigest.isEqual(expected, derive(pin, salt))
    }

    private fun derive(pin: String, salt: ByteArray): ByteArray {
        val spec = PBEKeySpec(pin.toCharArray(), salt, 150_000, 256)
        return try {
            SecretKeyFactory.getInstance("PBKDF2WithHmacSHA1").generateSecret(spec).encoded
        } finally {
            spec.clearPassword()
        }
    }
}
