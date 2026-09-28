package ru.rassvet.chat

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.URI
import java.net.URL
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.UUID
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

internal data class TrustedNode(val url: String, val pin: ByteArray) {
    companion object {
        fun fromJson(value: JSONObject): TrustedNode {
            val uri = URI(value.getString("url"))
            require(uri.scheme == "https" && !uri.host.isNullOrEmpty() && uri.rawUserInfo == null &&
                uri.rawPath.isNullOrEmpty() && uri.rawQuery == null && uri.rawFragment == null) {
                "Неверный адрес узла"
            }
            val pin = Base64.decode(value.getString("tls_spki_sha256"), Base64.DEFAULT)
            require(pin.size == 32) { "Неверный ключ узла" }
            return TrustedNode(uri.toString(), pin)
        }
    }
}

internal class NodeHttpException(val status: Int) : IllegalStateException("Ошибка узла: $status")

internal class DeviceClient(private val context: Context) {
    private val preferences = context.getSharedPreferences("device", Context.MODE_PRIVATE)
    private val keyAlias = "rassvet-device"
    private val nodeLock = Any()
    private val sessionLock = Any()
    private var sessionUnlocked = false
    private var selectedNode: TrustedNode? = null
    private var nodeGeneration = 0L

    fun isActivated(): Boolean = preferences.contains("user_id")

    fun unlock(pin: String): Boolean = synchronized(sessionLock) {
        sessionUnlocked = isActivated() && PinLock(context).matches(pin)
        sessionUnlocked
    }

    fun lock() = synchronized(sessionLock) { sessionUnlocked = false }

    private fun requireUnlocked() {
        synchronized(sessionLock) { check(sessionUnlocked) { "Требуется PIN" } }
    }

    fun userID(): String = preferences.getString("user_id", "") ?: ""

    fun role(): String = preferences.getString("role", "") ?: ""

    fun refreshRole(): Boolean {
        val latest = JSONObject(get("/v1/me")).getString("role")
        if (latest == role()) return false
        preferences.edit().putString("role", latest).apply()
        return true
    }

    fun trustedNodeUrls(): List<String> = nodes().map { it.url }

    fun preferredNodeIndex(): Int = preferences.getInt("preferred_node", 0).coerceIn(0, 1)

    fun selectNode(index: Int) {
        requireUnlocked()
        val available = nodes()
        require(index in available.indices) { "Неверный узел" }
        synchronized(nodeLock) {
            nodeGeneration++
            selectedNode = available[index]
            preferences.edit().putInt("preferred_node", index).apply()
        }
    }

    private fun nodes(): List<TrustedNode> {
        val raw = org.json.JSONArray(preferences.getString("nodes", "[]"))
        return (0 until raw.length()).map { TrustedNode.fromJson(raw.getJSONObject(it)) }
    }

    fun activate(ticketText: String): String {
        require(!isActivated()) { "Телефон уже активирован" }
        val ticket = JSONObject(ticketText)
        val code = ticket.getString("code")
        val issuer = ticket.getString("issuer")
        val rawNodes = ticket.getJSONArray("nodes")
        require(code.isNotBlank() && rawNodes.length() == 2) { "Неверные данные активации" }
        val nodes = (0 until 2).map { TrustedNode.fromJson(rawNodes.getJSONObject(it)) }
        require(nodes[0].url != nodes[1].url && nodes.any { it.url == issuer }) { "Неверный узел выдачи" }
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        if (!keyStore.containsAlias(keyAlias)) {
            KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore").apply {
                initialize(
                    KeyGenParameterSpec.Builder(keyAlias, KeyProperties.PURPOSE_SIGN or KeyProperties.PURPOSE_VERIFY)
                        .setAlgorithmParameterSpec(java.security.spec.ECGenParameterSpec("secp256r1"))
                        .setDigests(KeyProperties.DIGEST_SHA256)
                        .build()
                )
            }.generateKeyPair()
        }
        val publicKey = keyStore.getCertificate(keyAlias).publicKey.encoded
        val deviceID = Uuid7.new()
        val body = JSONObject()
            .put("code", code)
            .put("device_id", deviceID)
            .put("public_key_p256", Base64.encodeToString(publicKey, Base64.NO_WRAP))
            .toString()
        val node = nodes.first { it.url == issuer }
        val connection = openPinned(node, "/v1/activation", "POST")
        try {
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
            connection.doOutput = true
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            val response = connection.inputStream.bufferedReader().use { it.readText() }
            val result = JSONObject(response)
            require(result.getString("device_id") == deviceID) { "Неверный ответ узла" }
            val saved = preferences.edit()
                .putString("user_id", result.getString("user_id"))
                .putString("device_id", deviceID)
                .putString("role", result.getString("role"))
                .putString("nodes", rawNodes.toString())
                .putInt("preferred_node", nodes.indexOf(node))
                .commit()
            check(saved) { "Не удалось сохранить привязку телефона" }
            return result.getString("user_id")
        } finally {
            connection.disconnect()
        }
    }

    fun get(path: String): String {
        requireUnlocked()
        require(isActivated()) { "Телефон не активирован" }
        require(path.startsWith("/v1/") && !path.startsWith("//")) { "Неверный путь" }
        val nodes = nodes()
        val (preferred, generation) = synchronized(nodeLock) { preferredNodeIndex() to nodeGeneration }
        var lastError: Exception? = null
        for (node in listOf(nodes[preferred], nodes[1 - preferred])) {
            try {
                val connection = openPinned(node, path, "GET")
                try {
                    sign(connection, "GET", path, ByteArray(0))
                    val status = connection.responseCode
                    if (status !in 200..299) throw NodeHttpException(status)
                    val result = connection.inputStream.bufferedReader().use { it.readText() }
                    synchronized(nodeLock) {
                        if (nodeGeneration == generation) {
                            nodeGeneration++
                            selectedNode = node
                            preferences.edit().putInt("preferred_node", nodes.indexOf(node)).apply()
                        }
                    }
                    return result
                } finally {
                    connection.disconnect()
                }
            } catch (error: java.io.IOException) {
                lastError = error
            }
        }
        throw lastError ?: IllegalStateException("Нет доверенных узлов")
    }

    fun post(path: String, json: JSONObject): String = writeJSON("POST", path, json)

    fun put(path: String, json: JSONObject): String = writeJSON("PUT", path, json)

    fun delete(path: String) {
        requireUnlocked()
        require(isActivated() && path.startsWith("/v1/") && !path.startsWith("//")) { "Неверный путь" }
        val node = writeNode()
        val connection = openPinned(node, path, "DELETE")
        try {
            sign(connection, "DELETE", path, ByteArray(0))
            check(connection.responseCode == 204) { "Ошибка узла: ${connection.responseCode}" }
        } finally {
            connection.disconnect()
        }
    }

    private fun writeJSON(method: String, path: String, json: JSONObject): String {
        requireUnlocked()
        require(isActivated()) { "Телефон не активирован" }
        require(path.startsWith("/v1/") && !path.startsWith("//")) { "Неверный путь" }
        val node = writeNode()
        val body = json.toString().toByteArray(Charsets.UTF_8)
        val connection = openPinned(node, path, method)
        try {
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
            sign(connection, method, path, body)
            connection.doOutput = true
            connection.setFixedLengthStreamingMode(body.size)
            connection.outputStream.use { it.write(body) }
            val status = connection.responseCode
            if (status !in 200..299) throw NodeHttpException(status)
            return connection.inputStream.bufferedReader().use { it.readText() }
        } finally {
            connection.disconnect()
        }
    }

    fun putAttachment(roomID: String, id: String, file: File, name: String, mimeType: String): String {
        requireUnlocked()
        require(isActivated() && file.length() in 1..(100L shl 20)) { "Недопустимый размер файла" }
        val path = "/v1/rooms/$roomID/attachments/$id"
        val hash = MessageDigest.getInstance("SHA-256")
        FileInputStream(file).use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                hash.update(buffer, 0, count)
            }
        }
        val digest = hash.digest().joinToString("") { "%02x".format(it) }
        val node = writeNode()
        val connection = openPinned(node, path, "PUT")
        try {
            connection.setRequestProperty("Content-Type", mimeType)
            connection.setRequestProperty("X-File-Name", name)
            connection.setRequestProperty("X-Content-SHA256", digest)
            sign(connection, "PUT", path, digest)
            connection.doOutput = true
            connection.setFixedLengthStreamingMode(file.length())
            connection.outputStream.use { output -> FileInputStream(file).use { it.copyTo(output) } }
            val status = connection.responseCode
            check(status in 200..299) { "Ошибка узла: $status" }
            return connection.inputStream.bufferedReader().use { it.readText() }
        } finally {
            connection.disconnect()
        }
    }

    fun downloadAttachment(roomID: String, id: String, size: Long, expectedHash: String,
                           file: File, progress: (Long) -> Unit) {
        requireUnlocked()
        require(isActivated() && size > 0 && expectedHash.matches(Regex("[0-9a-f]{64}"))) {
            "Неверные метаданные вложения"
        }
        val path = "/v1/rooms/$roomID/attachments/$id"
        val node = writeNode()
        val connection = openPinned(node, path, "GET")
        try {
            sign(connection, "GET", path, ByteArray(0))
            check(connection.responseCode == 200) { "Ошибка узла: ${connection.responseCode}" }
            check(connection.getHeaderField("Content-Length")?.toLongOrNull() == size &&
                connection.getHeaderField("X-Content-SHA256") == expectedHash) {
                "Метаданные файла не совпадают"
            }
            val hash = MessageDigest.getInstance("SHA-256")
            var bytes = 0L
            connection.inputStream.use { input ->
                FileOutputStream(file).use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val count = input.read(buffer)
                        if (count < 0) break
                        bytes += count
                        check(bytes <= size) { "Файл длиннее заявленного" }
                        hash.update(buffer, 0, count)
                        output.write(buffer, 0, count)
                        progress(bytes)
                    }
                }
            }
            check(bytes == size && hash.digest().joinToString("") { "%02x".format(it) } == expectedHash) {
                "Файл повреждён"
            }
        } catch (error: Exception) {
            file.delete()
            throw error
        } finally {
            connection.disconnect()
        }
    }

    private fun sign(connection: HttpsURLConnection, method: String, path: String, body: ByteArray) {
        val hash = MessageDigest.getInstance("SHA-256").digest(body).joinToString("") { "%02x".format(it) }
        sign(connection, method, path, hash)
    }

    private fun writeNode(): TrustedNode = synchronized(nodeLock) {
        selectedNode ?: nodes()[preferredNodeIndex()]
    }

    private fun sign(connection: HttpsURLConnection, method: String, path: String, bodyHash: String) {
        synchronized(sessionLock) {
            check(sessionUnlocked) { "Требуется PIN" }
            val authorization = "Device ${preferences.getString("user_id", "")}:${preferences.getString("device_id", "")}"
            val timestamp = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
                timeZone = TimeZone.getTimeZone("UTC")
            }.format(Date())
            val nonceBytes = ByteArray(24).also { SecureRandom().nextBytes(it) }
            val nonce = Base64.encodeToString(nonceBytes, Base64.NO_WRAP)
            val message = listOf(method, path, authorization, timestamp, nonce, bodyHash).joinToString("\n")
            val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            val key = keyStore.getKey(keyAlias, null) as java.security.PrivateKey
            val signature = java.security.Signature.getInstance("SHA256withECDSA").apply {
                initSign(key)
                update(message.toByteArray(Charsets.UTF_8))
            }.sign()
            connection.setRequestProperty("Authorization", authorization)
            connection.setRequestProperty("X-Time", timestamp)
            connection.setRequestProperty("X-Nonce", nonce)
            connection.setRequestProperty("X-Body-SHA256", bodyHash)
            connection.setRequestProperty("X-Signature", Base64.encodeToString(signature, Base64.NO_WRAP))
        }
    }

    private fun openPinned(node: TrustedNode, path: String, method: String): HttpsURLConnection {
        val uri = URI(node.url + path)
        val connection = URL(uri.toString()).openConnection() as HttpsURLConnection
        val trust = object : X509TrustManager {
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
                throw CertificateException("Клиентский сертификат не ожидается")
            }
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                if (chain.size != 1) throw CertificateException("Неверная цепочка узла")
                chain[0].checkValidity()
                val actual = MessageDigest.getInstance("SHA-256").digest(chain[0].publicKey.encoded)
                if (!MessageDigest.isEqual(actual, node.pin)) throw CertificateException("Недоверенный узел")
            }
        }
        connection.sslSocketFactory = SSLContext.getInstance("TLS").apply {
            init(null, arrayOf<TrustManager>(trust), null)
        }.socketFactory
        connection.hostnameVerifier = HttpsURLConnection.getDefaultHostnameVerifier()
        connection.requestMethod = method
        connection.connectTimeout = 5_000
        connection.readTimeout = 20_000
        return connection
    }
}
