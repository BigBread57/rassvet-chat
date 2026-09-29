package ru.rassvet.chat

import androidx.activity.ComponentActivity
import androidx.activity.ComponentDialog
import androidx.activity.OnBackPressedCallback
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.ComposeView
import androidx.core.view.WindowCompat
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.BitmapFactory
import android.graphics.Bitmap
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.Manifest
import android.net.Uri
import android.database.Cursor
import android.media.MediaScannerConnection
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.os.Bundle
import android.os.Handler
import android.text.InputType
import android.view.WindowManager
import android.view.Gravity
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.VideoView
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.ColorDrawable
import com.google.zxing.BarcodeFormat
import com.google.zxing.MultiFormatWriter
import com.google.zxing.MultiFormatReader
import com.google.zxing.BinaryBitmap
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.integration.android.IntentIntegrator
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.text.SimpleDateFormat
import java.util.Locale
import java.util.TimeZone
import java.util.UUID

class MainActivity : ComponentActivity() {
    companion object { private var cacheCleaned = false }

    private var darkTheme = true
    private val blue get() = if (darkTheme) Color.rgb(101, 184, 241) else Color.rgb(35, 151, 221)
    private val muted get() = if (darkTheme) Color.rgb(169, 182, 195) else Color.rgb(126, 138, 148)
    private val screenColor get() = if (darkTheme) Color.rgb(13, 20, 27) else Color.WHITE
    private val panelColor get() = if (darkTheme) Color.rgb(30, 41, 53) else Color.rgb(247, 249, 250)

    private lateinit var client: DeviceClient
    private lateinit var pinLock: PinLock
    private lateinit var devicePreferences: SharedPreferences
    private lateinit var content: LinearLayout
    private lateinit var footer: LinearLayout
    private lateinit var root: LinearLayout
    private lateinit var scroller: ScrollView
    private var unlocked = false
    @Volatile private var active = false
    private var screenVersion = 0
    private var backAction: (() -> Unit)? = null
    private var pendingAttachmentRoom: Pair<String, String>? = null
    private var pendingAttachmentUri: Uri? = null
    private var externalActivityPending = false
    private var pendingStorageDownload: (() -> Unit)? = null
    private var pendingScannedTicket: String? = null
    private var pendingActivationImage: Uri? = null
    private val downloadedFiles = mutableListOf<File>()
    private val videos = mutableListOf<VideoView>()
    private val expiryHandler = Handler(android.os.Looper.getMainLooper())
    private var enteredRoomID: String? = null
    private var roomMemberNames: Map<String, String> = emptyMap()
    private var errorDialogVisible = false
    private val activationListener = SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
        if (key == "user_id" && active && !unlocked && client.isActivated()) {
            if (pinLock.isSet()) showUnlock() else showPinSetup()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        darkTheme = getSharedPreferences("appearance", MODE_PRIVATE).getBoolean("dark_theme", true)
        setTheme(if (darkTheme) R.style.AppThemeDark else R.style.AppTheme)
        super.onCreate(savedInstanceState)
        // The acceptance test needs screenshots from the debug APK.
        if ((applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) == 0) {
            window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        }
        client = DeviceClient(this)
        pinLock = PinLock(this)
        devicePreferences = getSharedPreferences("device", MODE_PRIVATE)
        devicePreferences.registerOnSharedPreferenceChangeListener(activationListener)
        val attachmentRoomID = savedInstanceState?.getString("attachment_room_id")
        val attachmentRoomName = savedInstanceState?.getString("attachment_room_name")
        if (attachmentRoomID != null && attachmentRoomName != null) {
            pendingAttachmentRoom = attachmentRoomID to attachmentRoomName
        }
        pendingAttachmentUri = savedInstanceState?.getString("attachment_uri")?.let(Uri::parse)
        content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(16.dp(), 8.dp(), 16.dp(), 24.dp())
        }
        footer = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        scroller = ScrollView(this).apply { addView(content) }
        root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            fitsSystemWindows = true
            if (android.os.Build.VERSION.SDK_INT >= 26)
                importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            setBackgroundColor(screenColor)
            addView(scroller,
                LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
            addView(footer)
        }
        setContentView(root)
        applySystemBars()
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                val action = backAction
                if (action != null) action() else finish()
            }
        })
        if (!cacheCleaned) {
            cacheDir.listFiles { file -> file.name.startsWith("download-") || file.name.startsWith("attachment-") }
                ?.forEach { it.delete() }
            cacheCleaned = true
        }
        File(filesDir, "shares").listFiles()?.forEach { file ->
            if ((file.name.substringBefore('-').toLongOrNull() ?: 0) <= System.currentTimeMillis()) file.delete()
        }
        if (client.isActivated()) {
            if (pinLock.isSet()) showUnlock() else showPinSetup()
        } else showActivation()
    }

    override fun onResume() {
        super.onResume()
        active = true
        if (externalActivityPending) {
            externalActivityPending = false
            lockSession()
        }
        if (!client.isActivated()) {
            showActivation()
            processScannedTicket()
            processActivationImage()
        } else if (!unlocked) {
            if (pinLock.isSet()) showUnlock() else showPinSetup()
        } else if (pendingAttachmentUri != null) processPickedAttachment()
    }

    override fun onPause() {
        active = false
        if (!externalActivityPending) lockSession()
        super.onPause()
    }

    private fun lockSession() {
        backAction = null
        client.lock()
        unlocked = false
        screenVersion++
        clearScreen()
        clearDownloads()
    }

    override fun onDestroy() {
        devicePreferences.unregisterOnSharedPreferenceChangeListener(activationListener)
        super.onDestroy()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        pendingAttachmentRoom?.let { (id, name) ->
            outState.putString("attachment_room_id", id)
            outState.putString("attachment_room_name", name)
        }
        pendingAttachmentUri?.let { outState.putString("attachment_uri", it.toString()) }
        super.onSaveInstanceState(outState)
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        externalActivityPending = false
        if (requestCode == 4) return
        if (requestCode == IntentIntegrator.REQUEST_CODE) {
            pendingScannedTicket = IntentIntegrator.parseActivityResult(requestCode, resultCode, data)?.contents
            if (active) processScannedTicket()
            return
        }
        if (requestCode == 3) {
            pendingActivationImage = if (resultCode == RESULT_OK) data?.data else null
            if (active) processActivationImage()
            return
        }
        if (requestCode != 1) return
        if (resultCode != RESULT_OK || data?.data == null) {
            pendingAttachmentRoom = null
            pendingAttachmentUri = null
            return
        }
        pendingAttachmentUri = data.data
        if (unlocked) processPickedAttachment()
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<String>,
                                            grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != 5) return
        externalActivityPending = false
        val retry = pendingStorageDownload
        pendingStorageDownload = null
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED && unlocked) retry?.invoke()
        else if (unlocked) showError("Для сохранения в Загрузки нужно разрешение на файлы")
    }

    private fun processScannedTicket() {
        val ticket = pendingScannedTicket ?: return
        pendingScannedTicket = null
        showActivation()
        text("Активация по QR-коду")
        activateTicket(ticket)
    }

    private fun processActivationImage() {
        val uri = pendingActivationImage ?: return
        pendingActivationImage = null
        showActivation()
        runNetwork({
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            (contentResolver.openInputStream(uri) ?: error("Не удалось открыть изображение"))
                .use { BitmapFactory.decodeStream(it, null, bounds) }
            check(bounds.outWidth > 0 && bounds.outHeight > 0) { "Неверный формат изображения" }
            val options = BitmapFactory.Options().apply {
                inSampleSize = generateSequence(1) { it * 2 }
                    .first { maxOf(bounds.outWidth, bounds.outHeight) / it <= 2048 }
            }
            val bitmap = contentResolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, options) }
                ?: error("Не удалось прочитать изображение")
            try {
                val pixels = IntArray(bitmap.width * bitmap.height)
                bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                val source = RGBLuminanceSource(bitmap.width, bitmap.height, pixels)
                MultiFormatReader().decode(BinaryBitmap(HybridBinarizer(source))).text
            } finally { bitmap.recycle() }
        }) { result ->
            result.onSuccess(::activateTicket)
                .onFailure { showError("Не удалось прочитать QR-код: ${it.message}") }
        }
    }

    private fun activateTicket(ticket: String) {
        runNetwork({ client.activate(ticket) }) { result ->
            result.onFailure { if (!client.isActivated()) showError("Ошибка активации: ${it.message}") }
        }
    }

    private fun processPickedAttachment() {
        val room = pendingAttachmentRoom ?: return
        val uri = pendingAttachmentUri ?: return
        pendingAttachmentRoom = null
        pendingAttachmentUri = null
        showRoom(room.first, room.second)
        val metadata = runCatching {
            val rawName = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
                ?.use { cursor: Cursor -> if (cursor.moveToFirst()) cursor.getString(0) else null } ?: "file"
            val name = rawName.substringAfterLast('/').substringAfterLast('\\')
                .filterNot { it == '\u0000' || it == '\r' || it == '\n' }.take(200)
                .let { if (it.isBlank() || it == "." || it == "..") "file" else it }
            name to (contentResolver.getType(uri) ?: "application/octet-stream")
        }.getOrElse {
            showError("Не удалось открыть файл: ${it.message}")
            return
        }
        val (name, mimeType) = metadata
        val attachmentID = Uuid7.new()
        runNetwork({
            val file = File.createTempFile("attachment-", ".tmp", cacheDir)
            try {
                contentResolver.openInputStream(uri)?.use { input ->
                    file.outputStream().use { output ->
                        val buffer = ByteArray(64 * 1024)
                        var bytes = 0L
                        while (true) {
                            val count = input.read(buffer)
                            if (count < 0) break
                            bytes += count
                            require(bytes <= (100L shl 20)) { "Файл больше 100 МиБ" }
                            output.write(buffer, 0, count)
                        }
                        require(bytes > 0) { "Файл пуст" }
                    }
                } ?: error("Не удалось прочитать файл")
                try {
                    client.putAttachment(room.first, attachmentID, file, name, mimeType)
                } catch (error: java.io.IOException) {
                    client.putAttachment(room.first, attachmentID, file, name, mimeType)
                }
            } finally {
                file.delete()
            }
        }) { result ->
            result.onSuccess { showRoom(room.first, room.second) }
                .onFailure { showError("Ошибка загрузки: ${it.message}") }
        }
    }

    private fun showActivation() {
        backAction = null
        screenVersion++
        clearScreen()
        title("Подключить Рассвет")
        text("Получите код и адреса двух узлов у администратора.")
        button("Сканировать QR-код", primary = true) {
            externalActivityPending = true
            IntentIntegrator(this).setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
                .setCaptureActivity(PortraitCaptureActivity::class.java)
                .setPrompt("Наведите камеру на код активации")
                .setBeepEnabled(false).initiateScan()
        }
        button("Загрузить фото QR-кода") {
            externalActivityPending = true
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = "image/*"
            }, 3)
        }
        button("Ручная настройка") { showManualActivation() }
    }

    private fun showManualActivation() {
        backAction = ::showActivation
        screenVersion++
        clearScreen()
        compose(content, fill = true) {
            ManualActivationScreen(::showActivation, ::activateTicket) {
                    code, firstUrl, firstPin, secondUrl, secondPin, secondIssuer ->
                val nodes = JSONArray()
                    .put(JSONObject().put("url", firstUrl.trim())
                        .put("tls_spki_sha256", firstPin.trim()))
                    .put(JSONObject().put("url", secondUrl.trim())
                        .put("tls_spki_sha256", secondPin.trim()))
                activateTicket(JSONObject().put("code", code.trim())
                    .put("issuer", if (secondIssuer) secondUrl.trim() else firstUrl.trim())
                    .put("nodes", nodes).toString())
            }
        }
    }

    private fun showPinSetup() {
        backAction = null
        screenVersion++
        clearScreen()
        title("Установите PIN")
        val form = formCard()
        val input = pinInput(form)
        button("Сохранить PIN", form, primary = true) {
            runCatching { pinLock.set(input.text.toString()); check(client.unlock(input.text.toString())) }
                .onSuccess { unlocked = true; showRooms() }
                .onFailure { input.error = it.message ?: "Проверьте PIN" }
        }
    }

    private fun showUnlock() {
        backAction = null
        screenVersion++
        clearScreen()
        title("Введите PIN")
        val form = formCard()
        val input = pinInput(form)
        button("Открыть", form, primary = true) {
            if (client.unlock(input.text.toString())) {
                unlocked = true
                when {
                    pendingAttachmentUri != null -> processPickedAttachment()
                    else -> showRooms()
                }
            } else {
                input.text.clear()
                input.error = "Неверный PIN"
            }
        }
    }

    private fun pinInput(parent: LinearLayout): EditText = EditText(this).also {
        text("PIN", parent)
        it.inputType = InputType.TYPE_CLASS_NUMBER or InputType.TYPE_NUMBER_VARIATION_PASSWORD
        it.contentDescription = "PIN"
        if (android.os.Build.VERSION.SDK_INT >= 26)
            it.importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO
        parent.addView(it)
    }

    private fun showRooms() {
        if (!unlocked) return
        backAction = null
        screenVersion++
        enteredRoomID = null
        clearDownloads()
        clearScreen()
        compose(content, fill = true) {
            ChatHeader("Чаты", onRefresh = ::showRooms,
                onCreate = if (client.role() == "admin") ::showCreateRoom else null,
                onSettings = ::showSettings)
        }
        compose(content, fill = true) { ChatHeaderDivider() }
        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        content.addView(list)
        loadRoomsPage(list)
        runNetwork({ client.refreshRole() }) { result ->
            result.onSuccess { if (it) showRooms() }
        }
    }

    private fun showSettings() {
        if (!unlocked) return
        backAction = ::showRooms
        screenVersion++
        clearDownloads()
        clearScreen()
        compose(content, fill = true) { ChatHeader("Настройки", onBack = ::showRooms) }
        section("Приложение")
        settingsRow("sun", "Внешний вид", "Светлая или тёмная тема") { showAppearanceSettings() }
        settingsRow("globe", "Подключение", "Доверенные узлы и выбранный адрес") { showConnectionSettings() }
        if (client.role() == "admin") {
            section("Управление")
            settingsRow("person", "Пользователи", "Учётные записи и коды активации") { showAdmin("users") }
            settingsRow("room", "Комнаты и права", "Участники и разрешения") { showAdmin("rooms") }
        }
    }

    private fun showAppearanceSettings() {
        if (!unlocked) return
        backAction = ::showSettings
        screenVersion++
        clearDownloads()
        clearScreen()
        compose(content, fill = true) { ChatHeader("Внешний вид", onBack = ::showSettings) }
        section("Тема")
        themeSelector()
    }

    private fun showConnectionSettings() {
        if (!unlocked) return
        backAction = ::showSettings
        screenVersion++
        clearDownloads()
        clearScreen()
        compose(content, fill = true) { ChatHeader("Подключение", onBack = ::showSettings) }
        section("Доверенные узлы")
        val nodeStatus = TextView(this).apply { textSize = 13f; setTextColor(muted) }
        content.addView(nodeStatus)
        client.trustedNodeUrls().forEachIndexed { index, url ->
            button("${if (index == client.preferredNodeIndex()) "●" else "○"} Узел ${index + 1}: $url") {
                client.selectNode(index)
                showConnectionSettings()
            }
        }
        nodeStatus.text = "Выбран узел ${client.preferredNodeIndex() + 1}. Адрес открывает API, а не сайт."
    }

    private fun settingsRow(icon: String, title: String, subtitle: String, action: () -> Unit) {
        compose(content, fill = true) { ChatSettingsRow(icon, title, subtitle, action) }
    }

    private fun loadRoomsPage(parent: LinearLayout, cursor: String = "") {
        val base = if (client.role() == "admin") "/v1/admin/rooms" else "/v1/rooms"
        val path = if (cursor.isEmpty()) base else "$base?cursor=$cursor"
        if (cursor.isEmpty()) text("Загрузка чатов…", parent)
        runNetwork({ JSONObject(client.get(path)) }) { result ->
            result.onSuccess { page ->
                if (cursor.isEmpty()) parent.removeAllViews()
                val rooms = page.getJSONArray("items")
                if (rooms.length() == 0 && cursor.isEmpty()) text("Доступных комнат нет", parent)
                for (index in 0 until rooms.length()) {
                    val room = rooms.getJSONObject(index)
                    val id = room.getString("id")
                    val name = room.getString("name")
                    roomCard(name, parent) {
                        if (client.role() != "admin") showRoom(id, name)
                        else runNetwork({
                            try {
                                client.get("/v1/rooms/$id")
                            } catch (error: NodeHttpException) {
                                if (error.status != 404) throw error
                                client.put("/v1/admin/rooms/$id/members/${client.userID()}", JSONObject()
                                    .put("read", true).put("send_text", true)
                                    .put("add_attachment", true).put("save_attachment", true))
                            }
                        }) { opened ->
                            opened.onSuccess { showRoom(id, name) }
                                .onFailure { showError("Не удалось открыть комнату: ${it.message}") }
                        }
                    }
                }
                val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                if (next.isNotEmpty()) {
                    val more = nativeButton("Показать ещё комнаты")
                    parent.addView(more)
                    more.setOnClickListener { parent.removeView(more); loadRoomsPage(parent, next) }
                }
            }.onFailure {
                if (cursor.isEmpty()) {
                    parent.removeAllViews()
                    text("Не удалось загрузить чаты", parent)
                }
                showError("Ошибка списка комнат: ${it.message}")
            }
        }
    }

    private fun showAdmin(page: String) {
        if (!unlocked || client.role() != "admin") return
        backAction = ::showSettings
        screenVersion++
        clearDownloads()
        clearScreen()
        val pageTitle = if (page == "users") "Пользователи" else "Комнаты и права"
        compose(content, fill = true) { ChatHeader(pageTitle, onBack = ::showSettings) }
        if (page == "users") {
        section("Новый пользователь")
        val createForm = formCard()
        val userName = labeledInput(createForm, "Имя пользователя")
        val roles = listOf("user", "commander", "admin")
        val role = mutableIntStateOf(0)
        compose(createForm, fill = true) {
            ChatSettingsRow("person", "Роль", userRole(roles[role.intValue])) {
                showAppModal("Роль пользователя", options = roles.map(::userRole),
                    selectedIndex = role.intValue, onOption = { role.intValue = it })
            }
        }
        val usersList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        button("Создать пользователя", createForm, primary = true) {
            val name = userName.text.toString().trim()
            if (name.isEmpty()) { userName.error = "Укажите имя"; return@button }
            val selectedRole = roles[role.intValue]
            runNetwork({
                JSONObject(client.post("/v1/admin/users", JSONObject()
                    .put("name", name).put("role", selectedRole)))
            }) { result ->
                result.onSuccess {
                    usersList.removeAllViews()
                    loadUserRows(usersList)
                    userName.text.clear()
                }.onFailure {
                    if (it is NodeHttpException && it.status == 409)
                        userName.error = "Пользователь с таким именем уже существует"
                    else showError("Ошибка создания: ${it.message}")
                }
            }
        }
        section("Существующие пользователи")
        content.addView(usersList)
        loadUserRows(usersList)
        }

        if (page == "rooms") showAdminRooms()

    }

    private fun formCard(parent: LinearLayout? = content): LinearLayout = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(14.dp(), 10.dp(), 14.dp(), 12.dp())
        background = GradientDrawable().apply {
            setColor(panelColor)
            cornerRadius = 16.dp().toFloat()
        }
        layoutParams = LinearLayout.LayoutParams(-1, -2).apply {
            setMargins(0, 4.dp(), 0, 8.dp())
        }
        parent?.addView(this)
    }

    private fun labeledInput(parent: LinearLayout, label: String, number: Boolean = false): EditText {
        text(label, parent)
        return EditText(this).apply {
            if (android.os.Build.VERSION.SDK_INT >= 26)
                importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO
            inputType = if (number) InputType.TYPE_CLASS_NUMBER
                else InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            contentDescription = label
            parent.addView(this)
        }
    }

    private fun roomRights(read: Boolean = true, send: Boolean = true,
                           add: Boolean = false, save: Boolean = false) = JSONObject()
        .put("read", read).put("send_text", send)
        .put("add_attachment", add).put("save_attachment", save)

    private fun showAppModal(title: String, message: String? = null,
                             options: List<String> = emptyList(), selectedIndex: Int? = null,
                             confirmLabel: String? = null, dismissLabel: String = "Отмена",
                              onOption: (Int) -> Unit = {}, onConfirm: () -> Unit = {},
                              onDismissed: () -> Unit = {},
                             body: @Composable () -> Unit = {}) {
        val dialog = ComponentDialog(this)
        dialog.setOnDismissListener { onDismissed() }
        dialog.setContentView(ComposeView(this).apply {
            if (android.os.Build.VERSION.SDK_INT >= 26)
                importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            setContent {
                ChatTheme(darkTheme) {
                    ChatModal(title, message, options, selectedIndex, confirmLabel, dismissLabel,
                        onOption = { index -> dialog.dismiss(); onOption(index) },
                        onConfirm = { dialog.dismiss(); onConfirm() },
                        onDismiss = { dialog.dismiss() }, body = body)
                }
            }
        })
        dialog.window?.setBackgroundDrawable(ColorDrawable(Color.TRANSPARENT))
        dialog.show()
        dialog.window?.setLayout(resources.displayMetrics.widthPixels * 9 / 10, -2)
    }

    private fun showError(message: String) {
        if (errorDialogVisible) return
        errorDialogVisible = true
        showAppModal("Ошибка", message, dismissLabel = "ОК",
            onDismissed = { errorDialogVisible = false })
    }

    private fun showRightsModal(title: String, initial: JSONObject, confirmLabel: String,
                                onSave: (JSONObject) -> Unit) {
        val keys = listOf("read", "send_text", "add_attachment", "save_attachment")
        val values = mutableStateListOf<Boolean>().apply { addAll(keys.map { initial.optBoolean(it) }) }
        showAppModal(title, confirmLabel = confirmLabel, onConfirm = {
            val rights = JSONObject()
            keys.forEachIndexed { index, key -> rights.put(key, values[index]) }
            onSave(rights)
        }, body = {
            ChatRightsFields(values) { index, checked -> values[index] = checked }
        })
    }

    private fun showAdminRooms() {
        section("Создать комнату")
        val create = formCard()
        text("Название и участники", create)
        button("Создать комнату", create, primary = true) { showCreateRoom() }
        section("Существующие комнаты")
        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        content.addView(list)
        loadAdminItems("/v1/admin/rooms", list) { room ->
            showManageRoom(room.getString("id"), room.getString("name"))
        }
    }

    private fun showCreateRoom() {
        if (!unlocked || client.role() != "admin") return
        backAction = { showAdmin("rooms") }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader("Создать комнату", onBack = { showAdmin("rooms") }) }
        val form = formCard()
        val name = labeledInput(form, "Название комнаты")
        val members = linkedMapOf<String, Pair<String, JSONObject>>()
        val selectedCount = TextView(this).apply { text = "Участники: 0"; setTextColor(muted) }
        form.addView(selectedCount)
        button("Добавить участников", form) {
            pickUsers(members.keys) { selected ->
                val previous = members.toMap()
                members.clear()
                selected.forEach { (id, userName) ->
                    members[id] = previous[id] ?: (userName to roomRights())
                }
                selectedCount.text = "Участники: ${members.size}"
            }
        }
        button("Создать комнату", form, primary = true) {
            val roomName = name.text.toString().trim()
            if (roomName.isEmpty()) { name.error = "Укажите название комнаты"; return@button }
            val grants = members.toMap()
            runNetwork({
                val room = JSONObject(client.post("/v1/admin/rooms", JSONObject().put("name", roomName)))
                val id = room.getString("id")
                val all = grants.toMutableMap()
                if (client.userID() !in all) {
                    all[client.userID()] = "Вы" to roomRights(true, true, true, true)
                }
                val failures = mutableListOf<String>()
                all.forEach { (user, entry) ->
                    runCatching { client.put("/v1/admin/rooms/$id/members/$user", entry.second) }
                        .onFailure { failures.add(entry.first) }
                }
                id to failures
            }) { result ->
                result.onSuccess { (id, failures) ->
                    showManageRoom(id, roomName)
                    if (failures.isNotEmpty()) showError("Не удалось добавить: ${failures.joinToString(", ")}")
                }.onFailure {
                    if (it is NodeHttpException && it.status == 409)
                        name.error = "Комната с таким названием уже существует"
                    else showError("Ошибка создания: ${it.message}")
                }
            }
        }
    }

    private fun pickUsers(initial: Collection<String>, selected: (List<Pair<String, String>>) -> Unit) {
        loadUsers { result ->
            result.onSuccess { users ->
                val choices = users.filter { it.getString("id") != client.userID() }
                    .map { it.getString("id") to
                        "${it.getString("name")} · ${userRole(it.getString("role"))} · ${shortId(it.getString("id"))}" }
                val picked = mutableStateListOf<String>().apply { addAll(initial) }
                showAppModal("Выберите участников", confirmLabel = "Готово", onConfirm = {
                    selected(users.filter { it.getString("id") in picked }
                        .map { it.getString("id") to it.getString("name") })
                }, body = {
                    ChatMultiUserPicker(choices, picked) { id ->
                        if (id in picked) picked.remove(id) else picked.add(id)
                    }
                })
            }.onFailure { showError("Ошибка списка пользователей: ${it.message}") }
        }
    }

    private fun showManageRoom(id: String, name: String, fromChat: Boolean = false) {
        if (!unlocked || client.role() != "admin") return
        val back = { if (fromChat) showRoomSettings(id, name) else showAdmin("rooms") }
        backAction = back
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader(name, onBack = back) }
        text("Комната · ${shortId(id)}")
        section("Участники и права")
        val card = formCard()
        button("Добавить участников", card, primary = true) {
            loadRoomMembers(id) { current ->
                current.onSuccess { existing ->
                    val known = existing.map { it.getString("user_id") }.toSet()
                    pickUsers(known) { selected ->
                        val additions = selected.filter { it.first !in known }
                        runNetwork({ additions.forEach { (user, _) ->
                            client.put("/v1/admin/rooms/$id/members/$user", roomRights())
                        } }) { result ->
                            result.onSuccess { showManageRoom(id, name, fromChat) }
                                .onFailure { showError("Ошибка добавления: ${it.message}") }
                        }
                    }
                }
                    .onFailure { showError("Ошибка участников: ${it.message}") }
            }
        }
        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        content.addView(list)
        text("Загрузка участников…", list)
        loadRoomMembers(id) { result ->
            result.onSuccess { members ->
                list.removeAllViews()
                if (members.isEmpty()) text("Участников пока нет", list)
                members.forEach { member ->
                    val user = member.getString("user_id")
                    val userName = member.getString("name")
                    compose(list, fill = true) {
                        ChatSettingsRow("person", userName,
                            "${userRole(member.getString("role"))} · ${shortId(user)}") {
                            showRoomMemberDetails(id, name, member, fromChat)
                        }
                    }
                }
            }.onFailure {
                list.removeAllViews()
                text("Не удалось загрузить участников", list)
                showError("Ошибка участников: ${it.message}")
            }
        }
    }

    private fun showRoomMemberDetails(roomID: String, roomName: String, member: JSONObject,
                                      fromChat: Boolean) {
        val userID = member.getString("user_id")
        val userName = member.getString("name")
        val back = { showManageRoom(roomID, roomName, fromChat) }
        backAction = back
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader(userName, onBack = back) }
        text("ID: ${shortId(userID)}")
        section("Роль")
        compose(content, fill = true) {
            ChatSettingsRow("person", userRole(member.getString("role")),
                "Во всех комнатах, где участвует", if (userID == client.userID()) null else {{
                    showRolePicker(userID, member.getString("role")) { role ->
                        showRoomMemberDetails(roomID, roomName,
                            JSONObject(member.toString()).put("role", role), fromChat)
                    }
                }})
        }
        section("Права в комнате")
        val rightsCard = formCard()
        val rights = member.getJSONObject("rights")
        if (member.getString("role") == "commander") {
            text("Капитан имеет полный доступ к комнатам", rightsCard)
        } else text(listOf(
            "Читать" to rights.optBoolean("read"),
            "Отправлять сообщения" to rights.optBoolean("send_text"),
            "Добавлять вложения" to rights.optBoolean("add_attachment"),
            "Сохранять вложения" to rights.optBoolean("save_attachment")
        ).filter { it.second }.joinToString(", ") { it.first }.ifEmpty { "Нет прав" }, rightsCard)
        if (member.getString("role") != "commander") button("Изменить права", rightsCard) {
            showRightsModal("Права · $userName", rights, "Сохранить") { updated ->
                runNetwork({ client.put("/v1/admin/rooms/$roomID/members/$userID", updated) }) { result ->
                    result.onSuccess {
                        showRoomMemberDetails(roomID, roomName,
                            JSONObject(member.toString()).put("rights", updated), fromChat)
                    }.onFailure { showError("Ошибка прав: ${it.message}") }
                }
            }
        }
        button("Удалить из комнаты", rightsCard) {
            showAppModal("Удалить из комнаты?", message = userName,
                confirmLabel = "Удалить", onConfirm = {
                    runNetwork({ client.delete("/v1/admin/rooms/$roomID/members/$userID") }) { result ->
                        result.onSuccess { back() }
                            .onFailure { showError("Ошибка удаления: ${it.message}") }
                    }
                })
        }
    }

    private fun loadRoomMembers(id: String, done: (Result<List<JSONObject>>) -> Unit) {
        runNetwork({
            val members = mutableListOf<JSONObject>()
            var cursor = ""
            do {
                val path = "/v1/admin/rooms/$id/members" + if (cursor.isEmpty()) "" else "?cursor=$cursor"
                val page = JSONObject(client.get(path))
                val items = page.getJSONArray("items")
                for (index in 0 until items.length()) members.add(items.getJSONObject(index))
                cursor = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
            } while (cursor.isNotEmpty())
            members
        }, done = done)
    }

    private fun loadUsers(done: (Result<List<JSONObject>>) -> Unit) {
        runNetwork({
            val users = mutableListOf<JSONObject>()
            var cursor = ""
            do {
                val path = "/v1/admin/users" + if (cursor.isEmpty()) "" else "?cursor=$cursor"
                val page = JSONObject(client.get(path))
                val items = page.getJSONArray("items")
                for (index in 0 until items.length()) users.add(items.getJSONObject(index))
                cursor = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
            } while (cursor.isNotEmpty())
            users.sortedBy { it.getString("name") }
        }, done = done)
    }

    private fun userRole(role: String): String = when (role) {
        "admin" -> "Администратор"
        "commander" -> "Капитан"
        else -> "Участник"
    }

    private fun loadUserRows(parent: LinearLayout) {
        loadUsers { result ->
            result.onSuccess { users ->
                parent.removeAllViews()
                if (users.isEmpty()) text("Список пуст", parent)
                users.forEach { user ->
                    val id = user.getString("id")
                    val name = user.getString("name")
                    compose(parent, fill = true) {
                        ChatSettingsRow("person", name,
                            userRole(user.getString("role")) + " · " + shortId(id)) {
                            showUserDetails(user)
                        }
                    }
                }
            }.onFailure { showError("Ошибка списка пользователей: ${it.message}") }
        }
    }

    private fun showUserDetails(user: JSONObject) {
        val id = user.getString("id")
        val name = user.getString("name")
        backAction = { showAdmin("users") }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader(name, onBack = { showAdmin("users") }) }
        text("ID: ${shortId(id)}")
        section("Роль пользователя")
        compose(content, fill = true) {
            ChatSettingsRow("person", userRole(user.getString("role")),
                "Во всех комнатах, где участвует", if (id == client.userID()) null else {{
                    showRolePicker(id, user.getString("role")) { role ->
                        showUserDetails(JSONObject(user.toString()).put("role", role))
                    }
                }})
        }
        if (id != client.userID() || !user.optBoolean("activated")) {
            section("Доступ")
            val accessCard = formCard()
            if (!user.optBoolean("activated")) {
                val ticketBox = formCard(null).apply { visibility = android.view.View.GONE }
                var issuingTicket = false
                button("Выпустить ключ активации", accessCard) {
                    if (!issuingTicket) {
                        issuingTicket = true
                        issueActivation(id, name, ticketBox) { issuingTicket = false }
                    }
                }
                content.addView(ticketBox)
            }
            if (id != client.userID()) button("Удалить пользователя", accessCard) {
                confirmDeleteUser(id, name)
            }
        }
    }

    private fun showRolePicker(id: String, current: String, saved: (String) -> Unit) {
        val roles = listOf("user", "commander", "admin")
        showAppModal("Роль пользователя", options = roles.map(::userRole),
            selectedIndex = roles.indexOf(current), onOption = { index ->
                val role = roles[index]
                if (role != current) {
                    runNetwork({ client.put("/v1/admin/users/$id", JSONObject().put("role", role)) }) { result ->
                        result.onSuccess { saved(role) }
                            .onFailure { showError("Не удалось изменить роль: ${it.message}") }
                    }
                }
            })
    }

    private fun issueActivation(id: String, name: String, ticketBox: LinearLayout,
                                finished: () -> Unit) {
        runNetwork({ client.post("/v1/admin/users/${UUID.fromString(id)}/activation",
            JSONObject().put("ttl_seconds", 600)) }) { result ->
            finished()
            result.onSuccess { ticket ->
                ticketBox.visibility = android.view.View.VISIBLE
                ticketBox.removeAllViews()
                ticketBox.tag = ticket
                text("Код активации для $name", ticketBox)
                button("Скопировать JSON", ticketBox) {
                    (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager)
                        .setPrimaryClip(ClipData.newPlainText("Ключ активации", ticket))
                }
                val expiresAt = runCatching {
                    SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
                        timeZone = TimeZone.getTimeZone("UTC")
                        isLenient = false
                    }.parse(JSONObject(ticket).getString("expires_at"))?.time
                }.getOrNull()
                if (expiresAt == null) {
                    ticketBox.removeAllViews()
                    text("Узел вернул неверный срок кода", ticketBox)
                    return@onSuccess
                }
                runCatching {
                    val matrix = MultiFormatWriter().encode(ticket, BarcodeFormat.QR_CODE, 600, 600)
                    val pixels = IntArray(600 * 600) { index ->
                        if (matrix[index % 600, index / 600]) Color.BLACK else Color.WHITE
                    }
                    Bitmap.createBitmap(pixels, 600, 600, Bitmap.Config.RGB_565)
                }.onSuccess { bitmap ->
                    ticketBox.addView(ImageView(this).apply { setImageBitmap(bitmap) })
                    button("Поделиться QR-кодом", ticketBox) { shareQrCode(bitmap, expiresAt) }
                }.onFailure { showError("Не удалось показать QR-код: ${it.message}") }
                expiryHandler.postDelayed({
                    if (ticketBox.tag == ticket) {
                        ticketBox.removeAllViews()
                        ticketBox.visibility = android.view.View.GONE
                    }
                }, (expiresAt - System.currentTimeMillis()).coerceAtLeast(0))
            }.onFailure { showError("Ошибка выдачи кода: ${it.message}") }
        }
    }

    private fun shareQrCode(bitmap: Bitmap, expiresAt: Long) {
        runCatching {
            val deadline = minOf(expiresAt, System.currentTimeMillis() + 10 * 60 * 1000)
            check(deadline > System.currentTimeMillis()) { "Срок кода истёк" }
            val dir = File(filesDir, "shares")
            check(dir.isDirectory || dir.mkdir()) { "Не удалось подготовить передачу" }
            val file = File(dir, "$deadline-${Uuid7.new()}")
            try {
                file.outputStream().use { check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
                val uri = Uri.Builder().scheme("content").authority("ru.rassvet.chat.share")
                    .appendPath(file.name).appendQueryParameter("name", "rassvet-qr.png")
                    .appendQueryParameter("type", "image/png").build()
                val intent = Intent(Intent.ACTION_SEND).apply {
                    type = "image/png"
                    putExtra(Intent.EXTRA_STREAM, uri)
                    clipData = ClipData.newUri(contentResolver, "QR-код", uri)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                Handler(mainLooper).postDelayed({ file.delete() }, deadline - System.currentTimeMillis())
                externalActivityPending = true
                startActivityForResult(Intent.createChooser(intent, "Поделиться QR-кодом").apply {
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }, 4)
            } catch (error: Exception) {
                file.delete()
                throw error
            }
        }.onFailure { showError("Не удалось поделиться QR-кодом: ${it.message}") }
    }

    private fun confirmDeleteUser(id: String, name: String) {
        showAppModal("Удалить пользователя?",
            message = "Доступ пользователя «$name» будет отозван. История сообщений сохранится.",
            confirmLabel = "Удалить", onConfirm = {
                runNetwork({ client.delete("/v1/admin/users/${UUID.fromString(id)}") }) { result ->
                    result.onSuccess { showAdmin("users") }
                        .onFailure { showError("Ошибка удаления: ${it.message}") }
                }
            })
    }

    private fun loadAdminItems(path: String, parent: LinearLayout, cursor: String = "",
                               select: (JSONObject) -> Unit) {
        val requestPath = if (cursor.isEmpty()) path else "$path?cursor=$cursor"
        if (cursor.isEmpty()) text("Загрузка списка…", parent)
        runNetwork({ JSONObject(client.get(requestPath)) }) { result ->
            result.onSuccess { page ->
                if (cursor.isEmpty()) parent.removeAllViews()
                val items = page.getJSONArray("items")
                if (items.length() == 0 && cursor.isEmpty()) text("Список пуст", parent)
                for (index in 0 until items.length()) {
                    val item = items.getJSONObject(index)
                    compose(parent, fill = true) {
                        ChatSettingsRow("room", item.getString("name"),
                            shortId(item.getString("id"))) { select(item) }
                    }
                }
                val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                if (next.isNotEmpty()) {
                    val more = nativeButton("Показать ещё")
                    parent.addView(more)
                    more.setOnClickListener {
                        parent.removeView(more)
                        loadAdminItems(path, parent, next, select)
                    }
                }
            }.onFailure {
                if (cursor.isEmpty()) {
                    parent.removeAllViews()
                    text("Не удалось загрузить список", parent)
                }
                showError("Ошибка списка: ${it.message}")
            }
        }
    }

    private fun showRoom(id: String, name: String) {
        if (!unlocked) return
        backAction = ::showRooms
        screenVersion++
        clearDownloads()
        clearScreen()
        compose(content, fill = true) {
            ChatHeader(name, onBack = ::showRooms,
                onSearch = { showMessageSearch(id, name) },
                onMore = { showRoomSettings(id, name) })
        }
        compose(content, fill = true) { ChatHeaderDivider() }
        if (client.role() == "commander" && enteredRoomID != id) {
            enteredRoomID = id
            runNetwork({ client.post("/v1/rooms/$id/presence", JSONObject().put("kind", "commander_entered")) }) { result ->
                result.onFailure {
                    enteredRoomID = null
                    showError("Не удалось сообщить о входе капитана: ${it.message}")
                }
            }
        }
        val path = "/v1/rooms/$id/messages"
        val loading = TextView(this).apply {
            text = "Загрузка сообщений…"
            textSize = 16f
            setTextColor(muted)
        }
        content.addView(loading)
        runNetwork({ client.get("/v1/rooms/$id") to client.get("$path?latest=1") }) { result ->
            result.onSuccess { (detailBody, historyBody) ->
                content.removeView(loading)
                val detail = JSONObject(detailBody)
                val members = detail.getJSONArray("members")
                roomMemberNames = (0 until members.length()).associate { index ->
                    val member = members.getJSONObject(index)
                    member.getString("user_id") to member.getString("name")
                }
                val eventsList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
                content.addView(eventsList)
                val seenEvents = mutableSetOf<String>()
                runNetwork({ JSONObject(client.get("/v1/rooms/$id/events?latest=1")) }) { events ->
                    events.onSuccess {
                        renderEventsPage(id, it, eventsList, seenEvents)
                        pollEvents(id, it.getString("cursor"), eventsList, seenEvents)
                    }.onFailure { showError("Ошибка событий: ${it.message}") }
                }
                val messagesList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
                content.addView(messagesList)
                val history = JSONObject(historyBody)
                val seenMessages = mutableSetOf<String>()
                val receiptLabels = mutableMapOf<String, TextView>()
                renderMessagePage(id, name, history, messagesList, seenMessages, receiptLabels)
                scrollToLatest(messagesList)
                pollMessages(id, name, history.getString("cursor"), messagesList, seenMessages, receiptLabels)
                pollReceiptLabels(id, receiptLabels)
                val canSave = detail.getJSONObject("rights").getBoolean("save_attachment")
                val attachmentsList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
                content.addView(attachmentsList)
                val seenAttachments = mutableSetOf<String>()
                runNetwork({ JSONObject(client.get("/v1/rooms/$id/attachments?latest=1")) }) { attachments ->
                    attachments.onSuccess {
                        renderAttachmentPage(id, canSave, it, attachmentsList, seenAttachments)
                        pollAttachments(id, canSave, it.getString("cursor"), attachmentsList, seenAttachments)
                    }.onFailure { showError("Ошибка вложений: ${it.message}") }
                }
                val rights = detail.getJSONObject("rights")
                val canAttach = rights.getBoolean("add_attachment") || client.role() == "commander"
                val canSendText = rights.getBoolean("send_text")
                if (canAttach || canSendText) {
                    var pendingID: String? = null
                    var pendingText = ""
                    footer.addView(ComposeView(this).apply {
                        layoutParams = LinearLayout.LayoutParams(-1, -2)
                        setContent {
                            var message by rememberSaveable { mutableStateOf("") }
                            var sending by remember { mutableStateOf(false) }
                            ChatTheme(darkTheme) {
                                ChatComposer(message, !sending, canSendText, canAttach,
                                    { message = it }, {
                                    pendingAttachmentRoom = id to name
                                    externalActivityPending = true
                                    startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                                        type = "*/*"
                                        addCategory(Intent.CATEGORY_OPENABLE)
                                    }, 1)
                                }) {
                                    if (message.isBlank()) return@ChatComposer
                                    if (pendingID == null) {
                                        pendingID = Uuid7.new()
                                        pendingText = message
                                    }
                                    sending = true
                                    val body = JSONObject().put("id", pendingID).put("text", pendingText)
                                    runNetwork({ JSONObject(client.post(path, body)) }) { sent ->
                                        sent.onSuccess { saved ->
                                            renderMessageItems(id, JSONObject().put("items", JSONArray().put(saved)),
                                                messagesList, seenMessages, receiptLabels)
                                            scrollToLatest(messagesList)
                                            message = ""
                                            pendingID = null
                                            pendingText = ""
                                            sending = false
                                        }
                                            .onFailure {
                                                 sending = false
                                                 showError("Не отправлено: ${it.message}. Нажмите «Отправить» для повтора.")
                                            }
                                    }
                                }
                            }
                        }
                    })
                }
            }.onFailure {
                loading.text = "Не удалось загрузить сообщения"
                showError("Ошибка: ${it.message}")
            }
        }
    }

    private fun showRoomSettings(id: String, name: String) {
        backAction = { showRoom(id, name) }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader("Настройки комнаты", onBack = { showRoom(id, name) }) }
        settingsRow("person", "Участники", "Состав и права комнаты") {
            if (client.role() == "admin") showManageRoom(id, name, fromChat = true)
            else showRoomParticipants(id, name)
        }
        if (client.role() == "admin") {
            settingsRow("edit", "Редактирование", "Название комнаты") { showRoomEdit(id, name) }
        } else compose(content, fill = true) {
            ChatSettingsRow("edit", "Редактирование", "Название меняет администратор")
        }
    }

    private fun showRoomParticipants(id: String, name: String) {
        backAction = { showRoomSettings(id, name) }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader("Участники", onBack = { showRoomSettings(id, name) }) }
        runNetwork({ JSONObject(client.get("/v1/rooms/$id")) }) { result ->
            result.onSuccess { detail ->
                val members = detail.getJSONArray("members")
                for (index in 0 until members.length()) {
                    val member = members.getJSONObject(index)
                    compose(content, fill = true) {
                        ChatSettingsRow("person", member.getString("name"),
                            shortId(member.getString("user_id")))
                    }
                }
            }.onFailure { showError("Ошибка участников: ${it.message}") }
        }
    }

    private fun showRoomEdit(id: String, name: String) {
        backAction = { showRoomSettings(id, name) }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader("Редактирование", onBack = { showRoomSettings(id, name) }) }
        section("Название комнаты")
        val form = formCard()
        val input = labeledInput(form, "Название комнаты").apply { setText(name) }
        button("Сохранить", form, primary = true) {
            val updated = input.text.toString().trim()
            if (updated.isEmpty()) { input.error = "Укажите название"; return@button }
            runNetwork({ client.put("/v1/admin/rooms/$id", JSONObject().put("name", updated)) }) { result ->
                result.onSuccess { showRoomSettings(id, updated) }
                    .onFailure {
                        if (it is NodeHttpException && it.status == 409)
                            input.error = "Комната с таким названием уже существует"
                        else showError("Не удалось переименовать комнату: ${it.message}")
                    }
            }
        }
    }

    private fun showMessageSearch(id: String, name: String) {
        backAction = { showRoom(id, name) }
        screenVersion++
        clearScreen()
        compose(content, fill = true) { ChatHeader("Поиск · $name", onBack = { showRoom(id, name) }) }
        val form = formCard()
        val query = labeledInput(form, "Поиск сообщений")
        val results = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        var searchToken = 0
        button("Найти", form, primary = true) {
            val phrase = query.text.toString().trim()
            if (phrase.isEmpty()) { query.error = "Введите запрос"; return@button }
            val token = ++searchToken
            results.removeAllViews()
            loadSearchPage(id, phrase, results, { token == searchToken })
        }
        content.addView(results)
    }

    private fun loadSearchPage(id: String, phrase: String, results: LinearLayout,
                               isCurrent: () -> Boolean, cursor: String = "") {
        val path = "/v1/rooms/$id/messages?q=${Uri.encode(phrase)}" +
            if (cursor.isEmpty()) "" else "&cursor=${Uri.encode(cursor)}"
        runNetwork({ JSONObject(client.get(path)) }) { result ->
            if (!isCurrent()) return@runNetwork
            result.onSuccess { page ->
                val items = page.getJSONArray("items")
                if (items.length() == 0 && cursor.isEmpty()) text("Сообщения не найдены", results)
                for (index in 0 until items.length()) {
                    val message = items.getJSONObject(index)
                    val sender = message.getString("sender_id")
                    val card = formCard(results)
                    text("${roomMemberNames[sender] ?: shortId(sender)} · ${message.getString("sent_at")}", card)
                    text(message.getString("text"), card)
                }
                val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                if (next.isNotEmpty()) {
                    val more = nativeButton("Показать ещё")
                    results.addView(more)
                    more.setOnClickListener {
                        results.removeView(more)
                        loadSearchPage(id, phrase, results, isCurrent, next)
                    }
                }
            }.onFailure { showError("Ошибка поиска: ${it.message}") }
        }
    }

    private fun pollMessages(roomID: String, roomName: String, cursor: String,
                             parent: LinearLayout, seen: MutableSet<String>,
                             receiptLabels: MutableMap<String, TextView>, delay: Long = 5000) {
        val version = screenVersion
        expiryHandler.postDelayed({
            if (!active || !unlocked || version != screenVersion) return@postDelayed
            runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/messages?since_rowid=$cursor")) }) { result ->
                result.onSuccess { page ->
                    renderMessageItems(roomID, page, parent, seen, receiptLabels)
                    val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                    pollMessages(roomID, roomName, if (next.isNotEmpty()) next else page.getString("cursor"),
                        parent, seen, receiptLabels, if (next.isNotEmpty()) 0 else 5000)
                }.onFailure {
                    if (it is NodeHttpException && it.status == 404) showRooms()
                    else pollMessages(roomID, roomName, cursor, parent, seen, receiptLabels)
                }
            }
        }, delay)
    }

    private fun renderMessagePage(roomID: String, roomName: String, page: JSONObject,
                                  parent: LinearLayout, seen: MutableSet<String>,
                                  receiptLabels: MutableMap<String, TextView>) {
        renderMessageItems(roomID, page, parent, seen, receiptLabels, true)
        val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
        if (next.isNotEmpty()) {
            val more = nativeButton("Показать ещё сообщения")
            parent.addView(more, 0)
            more.setOnClickListener {
                parent.removeView(more)
                runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/messages?cursor=$next")) }) { result ->
                    result.onSuccess { renderMessagePage(roomID, roomName, it, parent, seen, receiptLabels) }
                        .onFailure { showError("Ошибка истории: ${it.message}") }
                }
            }
        }
    }

    private fun renderMessageItems(roomID: String, page: JSONObject,
                                   parent: LinearLayout, seen: MutableSet<String>,
                                   receiptLabels: MutableMap<String, TextView>, prepend: Boolean = false) {
        val messages = page.getJSONArray("items")
        val received = mutableListOf<Pair<String, Long>>()
        var insertAt = 0
        for (index in 0 until messages.length()) {
            val message = messages.getJSONObject(index)
            val messageID = message.getString("id")
            if (!seen.add(messageID)) continue
            val expiresAt = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
                timeZone = TimeZone.getTimeZone("UTC")
                isLenient = false
            }.parse(message.getString("expires_at"))?.time ?: continue
            if (expiresAt <= System.currentTimeMillis()) continue
            val mine = message.getString("sender_id") == client.userID()
            val row = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(12.dp(), 8.dp(), 12.dp(), 8.dp())
                background = GradientDrawable().apply {
                    setColor(if (mine) {
                        if (darkTheme) Color.rgb(30, 66, 88) else Color.rgb(223, 242, 253)
                    } else panelColor)
                    cornerRadius = 16.dp().toFloat()
                }
                layoutParams = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT)
                    .apply {
                        gravity = if (mine) Gravity.END else Gravity.START
                        setMargins(4.dp(), 5.dp(), 4.dp(), 5.dp())
                    }
            }
            if (prepend) parent.addView(row, insertAt++) else parent.addView(row)
            val sender = message.getString("sender_id")
            row.addView(TextView(this).apply {
                text = if (mine) "Вы" else roomMemberNames[sender] ?: shortId(sender)
                textSize = 13f
                setTypeface(null, Typeface.BOLD)
                setTextColor(blue)
            })
            text(message.getString("text"), row)
            expiryHandler.postDelayed({ parent.removeView(row); receiptLabels.remove(messageID) }, expiresAt - System.currentTimeMillis())
            if (message.getString("sender_id") != client.userID()) {
                val receipts = message.optJSONArray("receipts")
                val mine = (0 until (receipts?.length() ?: 0)).firstOrNull {
                    receipts!!.getJSONObject(it).getString("user_id") == client.userID()
                }?.let { receipts!!.getJSONObject(it) }
                if (mine == null) received.add(messageID to expiresAt)
                if (mine?.optString("state") == "read") {
                    text("Прочитано", row)
                } else {
                    val readButton = nativeButton("Отметить прочитанным")
                    row.addView(readButton)
                    readButton.setOnClickListener {
                        readButton.isEnabled = false
                        runNetwork({ client.post("/v1/rooms/$roomID/messages/$messageID/receipts", JSONObject().put("state", "read")) }) { saved ->
                            saved.onSuccess { row.removeView(readButton); text("Прочитано", row) }
                                .onFailure { readButton.isEnabled = true; showError("Не удалось подтвердить прочтение: ${it.message}") }
                        }
                    }
                }
            } else {
                val label = TextView(this).apply {
                    text = receiptLabel(message.optJSONArray("receipts"))
                    textSize = 12f
                    setTextColor(muted)
                }
                row.addView(label)
                receiptLabels[messageID] = label
            }
        }
        if (received.isNotEmpty()) {
            sendDelivered(roomID, received, screenVersion)
        }
    }

    private fun sendDelivered(roomID: String, messages: List<Pair<String, Long>>, version: Int) {
        if (!active || !unlocked || version != screenVersion) return
        val pending = messages.filter { it.second > System.currentTimeMillis() }
        if (pending.isEmpty()) return
        runNetwork({ pending.forEach { (messageID, _) ->
            client.post("/v1/rooms/$roomID/messages/$messageID/receipts", JSONObject().put("state", "delivered"))
        } }) { result ->
            result.onFailure {
                expiryHandler.postDelayed({ sendDelivered(roomID, pending, version) }, 5000)
            }
        }
    }

    private fun receiptLabel(receipts: JSONArray?): String {
        if (receipts == null || receipts.length() == 0) return "Сохранено на узле"
        return (0 until receipts.length()).joinToString(", ") {
            val entry = receipts.getJSONObject(it)
            val state = when (entry.getString("state")) {
                "read" -> "прочитано"
                "delivered" -> "доставлено"
                else -> "сохранено"
            }
            "${entry.getString("user_id")}: $state"
        }
    }

    private fun pollReceiptLabels(roomID: String, labels: MutableMap<String, TextView>) {
        val version = screenVersion
        expiryHandler.postDelayed({
            if (!active || !unlocked || version != screenVersion) return@postDelayed
            if (labels.isEmpty()) pollReceiptLabels(roomID, labels)
            else loadReceiptLabels(roomID, labels, labels.keys.toMutableSet(), "")
        }, 15000)
    }

    private fun loadReceiptLabels(roomID: String, labels: MutableMap<String, TextView>,
                                  remaining: MutableSet<String>, cursor: String) {
        val path = if (cursor.isEmpty()) "/v1/rooms/$roomID/messages?latest=1&limit=100&receipts_only=1"
            else "/v1/rooms/$roomID/messages?cursor=$cursor&limit=100&receipts_only=1"
        runNetwork({ JSONObject(client.get(path)) }) { result ->
            result.onSuccess { page ->
                val items = page.getJSONArray("items")
                for (index in 0 until items.length()) {
                    val item = items.getJSONObject(index)
                    val id = item.getString("id")
                    labels[id]?.text = receiptLabel(item.optJSONArray("receipts"))
                    remaining.remove(id)
                }
                val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                if (next.isNotEmpty() && remaining.isNotEmpty()) loadReceiptLabels(roomID, labels, remaining, next)
                else pollReceiptLabels(roomID, labels)
            }.onFailure {
                if (it is NodeHttpException && it.status == 404) showRooms()
                else pollReceiptLabels(roomID, labels)
            }
        }
    }

    private fun pollAttachments(roomID: String, canSave: Boolean, cursor: String,
                                parent: LinearLayout, seen: MutableSet<String>, delay: Long = 15000) {
        val version = screenVersion
        expiryHandler.postDelayed({
            if (!active || !unlocked || version != screenVersion) return@postDelayed
            runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/attachments?since_rowid=$cursor")) }) { result ->
                result.onSuccess { page ->
                    renderAttachmentItems(roomID, canSave, page, parent, seen)
                    val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                    pollAttachments(roomID, canSave, if (next.isNotEmpty()) next else page.getString("cursor"),
                        parent, seen, if (next.isNotEmpty()) 0 else 15000)
                }.onFailure {
                    if (it is NodeHttpException && it.status == 404) showRooms()
                    else pollAttachments(roomID, canSave, cursor, parent, seen)
                }
            }
        }, delay)
    }

    private fun renderAttachmentPage(roomID: String, canSave: Boolean, page: JSONObject,
                                     parent: LinearLayout, seen: MutableSet<String>) {
        renderAttachmentItems(roomID, canSave, page, parent, seen, true)
        val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
        if (next.isNotEmpty()) {
            val more = nativeButton("Показать ещё вложения")
            parent.addView(more, 0)
            more.setOnClickListener {
                parent.removeView(more)
                runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/attachments?cursor=$next")) }) { result ->
                    result.onSuccess { renderAttachmentPage(roomID, canSave, it, parent, seen) }
                        .onFailure { showError("Ошибка вложений: ${it.message}") }
                }
            }
        }
    }

    private fun renderAttachmentItems(roomID: String, canSave: Boolean, page: JSONObject,
                                      parent: LinearLayout, seen: MutableSet<String>, prepend: Boolean = false) {
        val attachments = page.getJSONArray("items")
        var insertAt = 0
        for (index in 0 until attachments.length()) {
            val attachment = attachments.getJSONObject(index)
            if (!seen.add(attachment.getString("id"))) continue
            val expiresAt = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
                timeZone = TimeZone.getTimeZone("UTC")
                isLenient = false
            }.parse(attachment.getString("expires_at"))?.time ?: continue
            if (expiresAt <= System.currentTimeMillis()) continue
            val row = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(10.dp(), 8.dp(), 10.dp(), 8.dp())
                background = GradientDrawable().apply {
                    setColor(panelColor)
                    cornerRadius = 14.dp().toFloat()
                }
                layoutParams = LinearLayout.LayoutParams(-1, -2).apply {
                    setMargins(0, 4.dp(), 0, 4.dp())
                }
            }
            if (prepend) parent.addView(row, insertAt++) else parent.addView(row)
            text("${attachment.getString("name")} (${attachment.getLong("size")} байт)", row)
            lateinit var downloadButton: ComposeView
            downloadButton = button("Загрузить: ${attachment.getString("name")}", row) {
                downloadButton.visibility = android.view.View.GONE
                downloadAttachment(roomID, attachment, canSave, row, downloadButton)
            }
            val expiryVersion = screenVersion
            expiryHandler.postDelayed({
                if (expiryVersion == screenVersion) {
                    parent.removeView(row)
                }
            }, expiresAt - System.currentTimeMillis())
        }
    }

    private fun pollEvents(roomID: String, cursor: String, parent: LinearLayout,
                           seen: MutableSet<String>, delay: Long = 5000) {
        val version = screenVersion
        expiryHandler.postDelayed({
            if (!active || !unlocked || version != screenVersion) return@postDelayed
            runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/events?since_rowid=$cursor")) }) { result ->
                result.onSuccess { page ->
                    renderEventItems(page, parent, seen)
                    val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
                    pollEvents(roomID, if (next.isNotEmpty()) next else page.getString("cursor"),
                        parent, seen, if (next.isNotEmpty()) 0 else 5000)
                }.onFailure {
                    if (it is NodeHttpException && it.status == 404) showRooms()
                    else pollEvents(roomID, cursor, parent, seen)
                }
            }
        }, delay)
    }

    private fun renderEventsPage(roomID: String, page: JSONObject, parent: LinearLayout,
                                 seen: MutableSet<String>) {
        renderEventItems(page, parent, seen, true)
        val next = if (page.isNull("next_cursor")) "" else page.getString("next_cursor")
        if (next.isNotEmpty()) {
            val more = nativeButton("Показать ещё события")
            parent.addView(more, 0)
            more.setOnClickListener {
                parent.removeView(more)
                runNetwork({ JSONObject(client.get("/v1/rooms/$roomID/events?cursor=$next")) }) { result ->
                    result.onSuccess { renderEventsPage(roomID, it, parent, seen) }
                        .onFailure { showError("Ошибка событий: ${it.message}") }
                }
            }
        }
    }

    private fun renderEventItems(page: JSONObject, parent: LinearLayout, seen: MutableSet<String>,
                                 prepend: Boolean = false) {
        val events = page.getJSONArray("items")
        var insertAt = 0
        for (index in 0 until events.length()) {
            val event = events.getJSONObject(index)
            if (!seen.add(event.getString("id")) || event.getString("kind") != "commander_entered") continue
            val label = TextView(this).apply {
                text = "Капитан ${event.getString("commander_id")} вошёл: ${event.getString("entered_at")}"
                textSize = 18f
            }
            if (prepend) parent.addView(label, insertAt++) else parent.addView(label)
        }
    }

    private fun downloadAttachment(roomID: String, attachment: JSONObject, canSave: Boolean,
                                   parent: LinearLayout, downloadButton: ComposeView) {
        if (canSave && Build.VERSION.SDK_INT < Build.VERSION_CODES.Q &&
            checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            downloadButton.visibility = android.view.View.VISIBLE
            pendingStorageDownload = {
                downloadButton.visibility = android.view.View.GONE
                downloadAttachment(roomID, attachment, canSave, parent, downloadButton)
            }
            externalActivityPending = true
            requestPermissions(arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE), 5)
            return
        }
        val expiresAt = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("UTC")
            isLenient = false
        }.parse(attachment.getString("expires_at"))?.time ?: return
        if (expiresAt <= System.currentTimeMillis()) return
        val id = attachment.getString("id")
        val size = attachment.getLong("size")
        val hash = attachment.getString("sha256")
        val mimeType = attachment.optString("mime_type", "application/octet-stream")
        val status = TextView(this).apply { text = "Загружено: 0 / $size байт" }
        parent.addView(status)
        val version = screenVersion
        Thread {
            val result = runCatching {
                val file = File.createTempFile("download-", ".tmp", cacheDir)
                try {
                    var reported = 0L
                    client.downloadAttachment(roomID, id, size, hash, file) { bytes ->
                        if (bytes - reported >= (1L shl 20) || bytes == size) {
                            reported = bytes
                            runOnUiThread {
                                if (active && unlocked && version == screenVersion) {
                                    status.text = "Загружено: $bytes / $size байт"
                                }
                            }
                        }
                    }
                    if (canSave) {
                        val rights = JSONObject(client.get("/v1/rooms/$roomID")).getJSONObject("rights")
                        check(rights.getBoolean("save_attachment")) { "Нет права сохранения" }
                        check(expiresAt > System.currentTimeMillis()) { "Срок вложения истёк" }
                        saveToDownloads(file, attachment.getString("name"), mimeType)
                    }
                    file
                } catch (error: Exception) {
                    file.delete()
                    throw error
                }
            }
            runOnUiThread {
                if (!active || !unlocked || version != screenVersion) {
                    result.getOrNull()?.delete()
                } else {
                    result.onSuccess { file ->
                        if (expiresAt <= System.currentTimeMillis()) {
                            file.delete()
                            status.text = "Срок вложения истёк"
                            return@onSuccess
                        }
                        downloadedFiles.add(file)
                        expiryHandler.postDelayed({
                            file.delete()
                            downloadedFiles.remove(file)
                        }, (expiresAt - System.currentTimeMillis()).coerceAtLeast(0))
                        status.text = if (canSave) "Сохранено в Загрузки" else "Файл получен, целостность проверена"
                        if (mimeType.startsWith("image/")) {
                            val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                            BitmapFactory.decodeFile(file.path, options)
                            options.inJustDecodeBounds = false
                            options.inSampleSize = 1
                            while (options.outWidth / options.inSampleSize > 1024 ||
                                options.outHeight / options.inSampleSize > 1024) options.inSampleSize *= 2
                            BitmapFactory.decodeFile(file.path, options)?.let { bitmap ->
                                parent.addView(ImageView(this).apply { setImageBitmap(bitmap) })
                            }
                        } else if (mimeType.startsWith("video/")) {
                            VideoView(this).apply {
                                layoutParams = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 600)
                                setVideoPath(file.path)
                                setOnPreparedListener { player -> player.isLooping = false }
                                start()
                            }.also {
                                videos.add(it)
                                parent.addView(it)
                                expiryHandler.postDelayed({ it.stopPlayback(); videos.remove(it) },
                                    (expiresAt - System.currentTimeMillis()).coerceAtLeast(0))
                            }
                        }
                        if (canSave) button("Передать", parent) {
                            shareAttachment(roomID, attachment, file)
                        }
                    }.onFailure {
                        status.text = "Ошибка загрузки: ${it.message}"
                        downloadButton.visibility = android.view.View.VISIBLE
                    }
                }
            }
        }.start()
    }

    private fun saveToDownloads(source: File, name: String, mimeType: String) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val values = ContentValues().apply {
                put(MediaStore.MediaColumns.DISPLAY_NAME, name)
                put(MediaStore.MediaColumns.MIME_TYPE, mimeType)
                put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS)
                put(MediaStore.MediaColumns.IS_PENDING, 1)
            }
            val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                ?: error("Не удалось создать файл в Загрузках")
            try {
                contentResolver.openOutputStream(uri)?.use { output ->
                    source.inputStream().use { it.copyTo(output) }
                } ?: error("Не удалось записать файл в Загрузки")
                values.clear()
                values.put(MediaStore.MediaColumns.IS_PENDING, 0)
                check(contentResolver.update(uri, values, null, null) == 1) { "Не удалось опубликовать файл" }
            } catch (error: Exception) {
                contentResolver.delete(uri, null, null)
                throw error
            }
        } else {
            val directory = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
            check(directory.isDirectory || directory.mkdirs()) { "Каталог Загрузки недоступен" }
            val stem = name.substringBeforeLast('.', name)
            val extension = name.substringAfterLast('.', "").let { if (it.isEmpty()) "" else ".$it" }
            var target = File(directory, name)
            var copy = 1
            while (!target.createNewFile()) target = File(directory, "$stem (${copy++})$extension")
            try {
                source.inputStream().use { input -> target.outputStream().use { input.copyTo(it) } }
                MediaScannerConnection.scanFile(this, arrayOf(target.path), arrayOf(mimeType), null)
            } catch (error: Exception) {
                target.delete()
                throw error
            }
        }
    }

    private fun shareAttachment(roomID: String, attachment: JSONObject, source: File) {
        val name = attachment.getString("name")
        val mimeType = attachment.optString("mime_type", "application/octet-stream")
        runNetwork({
            val expires = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
                timeZone = TimeZone.getTimeZone("UTC")
                isLenient = false
            }.parse(attachment.getString("expires_at"))?.time ?: error("Неверный срок файла")
            val deadline = minOf(expires, System.currentTimeMillis() + 10 * 60 * 1000)
            val rights = JSONObject(client.get("/v1/rooms/$roomID")).getJSONObject("rights")
            check(rights.getBoolean("save_attachment") && deadline > System.currentTimeMillis()) {
                "Нет права передачи или срок файла истёк"
            }
            val dir = File(filesDir, "shares")
            check(dir.isDirectory || dir.mkdir()) { "Не удалось подготовить передачу" }
            val target = File(dir, "$deadline-${Uuid7.new()}")
            try {
                source.inputStream().use { input -> target.outputStream().use { input.copyTo(it) } }
                target to deadline
            } catch (error: Exception) {
                target.delete()
                throw error
            }
        }, { (file, _) -> file.delete() }) { result ->
            result.onSuccess { (file, deadline) ->
                val uri = Uri.Builder().scheme("content").authority("ru.rassvet.chat.share")
                    .appendPath(file.name).appendQueryParameter("name", name)
                    .appendQueryParameter("type", mimeType).build()
                val intent = Intent(Intent.ACTION_SEND).apply {
                    type = mimeType
                    putExtra(Intent.EXTRA_STREAM, uri)
                    clipData = ClipData.newUri(contentResolver, name, uri)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                Handler(mainLooper).postDelayed({ file.delete() }, (deadline - System.currentTimeMillis()).coerceAtLeast(0))
                externalActivityPending = true
                startActivityForResult(Intent.createChooser(intent, "Передать файл").apply {
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }, 4)
            }.onFailure { showError("Не удалось передать: ${it.message}") }
        }
    }

    private fun clearDownloads() {
        expiryHandler.removeCallbacksAndMessages(null)
        videos.forEach { it.stopPlayback() }
        videos.clear()
        downloadedFiles.forEach { it.delete() }
        downloadedFiles.clear()
    }

    private fun scrollToLatest(messages: LinearLayout) {
        val version = screenVersion
        scroller.post {
            if (version == screenVersion)
                scroller.smoothScrollTo(0, (messages.bottom - scroller.height + 12.dp()).coerceAtLeast(0))
        }
    }

    private fun Int.dp(): Int = (this * resources.displayMetrics.density).toInt()

    private fun nativeButton(label: String): Button = Button(this).apply {
        text = label
        isAllCaps = false
        minHeight = 48.dp()
        setTextColor(blue)
        background = GradientDrawable().apply {
            setColor(panelColor)
            setStroke(1.dp(), blue)
            cornerRadius = 10.dp().toFloat()
        }
    }

    private fun clearScreen() {
        content.removeAllViews()
        footer.removeAllViews()
    }

    private fun applySystemBars() {
        window.statusBarColor = screenColor
        window.navigationBarColor = if (!darkTheme && Build.VERSION.SDK_INT < 26) Color.BLACK else screenColor
        WindowCompat.getInsetsController(window, root).apply {
            isAppearanceLightStatusBars = !darkTheme
            isAppearanceLightNavigationBars = !darkTheme
        }
    }

    private fun themeSelector() {
        compose(content, fill = true) {
            ChatThemeSelector(darkTheme) { selected ->
                if (selected == darkTheme) return@ChatThemeSelector
                darkTheme = selected
                getSharedPreferences("appearance", MODE_PRIVATE).edit()
                    .putBoolean("dark_theme", selected).apply()
                setTheme(if (selected) R.style.AppThemeDark else R.style.AppTheme)
                root.setBackgroundColor(screenColor)
                applySystemBars()
                showAppearanceSettings()
            }
        }
    }

    private fun compose(parent: LinearLayout, fill: Boolean = false, horizontal: Boolean = false,
                        body: @Composable () -> Unit): ComposeView {
        return ComposeView(this).apply {
            if (android.os.Build.VERSION.SDK_INT >= 26)
                importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            layoutParams = LinearLayout.LayoutParams(
                if (horizontal) 0 else if (fill) -1 else -2, -2,
                if (horizontal) 1f else 0f)
            setContent { ChatTheme(darkTheme, body) }
        }.also(parent::addView)
    }

    private fun title(value: String) {
        compose(content) { ChatTitle(value) }
    }

    private fun section(value: String) {
        compose(content) { ChatSection(value) }
    }

    private fun roomCard(name: String, parent: LinearLayout, action: () -> Unit) {
        compose(parent, fill = true) { ChatRoomRow(name, action) }
    }

    private fun text(value: String, parent: LinearLayout = content) {
        compose(parent) { ChatText(value) }
    }

    private fun button(label: String, parent: LinearLayout = content, primary: Boolean = false,
                       action: () -> Unit): ComposeView {
        val horizontal = parent.orientation == LinearLayout.HORIZONTAL
        return compose(parent, fill = !horizontal, horizontal = horizontal) {
            ChatButton(label, horizontal, primary, action)
        }
    }

    private fun <T> runNetwork(work: () -> T, discard: (T) -> Unit = {}, done: (Result<T>) -> Unit) {
        val version = screenVersion
        Thread {
            val result = runCatching(work)
            runOnUiThread {
                if (active && version == screenVersion) done(result)
                else result.getOrNull()?.let(discard)
            }
        }.start()
    }
}
