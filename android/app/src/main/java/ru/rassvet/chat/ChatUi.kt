package ru.rassvet.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import kotlin.math.cos
import kotlin.math.sin
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.tooling.preview.Preview

private val Blue = Color(0xFF2397DD)
private val DarkBlue = Color(0xFF65B8F1)
private val LightColors = lightColorScheme(
    primary = Blue, onPrimary = Color.White, background = Color.White,
    onBackground = Color(0xFF1B2730), surface = Color.White,
    onSurface = Color(0xFF1B2730), onSurfaceVariant = Color(0xFF7E8A94),
    surfaceVariant = Color(0xFFF2F6F8), outlineVariant = Color(0xFFEAEEF1)
)
private val DarkColors = darkColorScheme(
    primary = DarkBlue, onPrimary = Color(0xFF07121C), background = Color(0xFF0D141B),
    onBackground = Color(0xFFF3F6F8), surface = Color(0xFF1E2935),
    onSurface = Color(0xFFF3F6F8), onSurfaceVariant = Color(0xFFA9B6C3),
    surfaceVariant = Color(0xFF202C39), outlineVariant = Color(0xFF344351)
)

@Composable
fun ChatTheme(dark: Boolean = true, content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (dark) DarkColors else LightColors, content = content)
}

@Composable
fun ChatTitle(value: String) {
    Text(value, modifier = Modifier.padding(start = 4.dp, top = 10.dp, bottom = 8.dp),
        fontSize = 24.sp, fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.onBackground)
}

@Composable
fun ChatIcon(name: String, modifier: Modifier = Modifier, tint: Color = MaterialTheme.colorScheme.onSurface) {
    Canvas(modifier.size(24.dp)) {
        val w = size.width
        val s = 2.dp.toPx()
        fun line(x1: Float, y1: Float, x2: Float, y2: Float) =
            drawLine(tint, Offset(x1 * w, y1 * w), Offset(x2 * w, y2 * w), s, StrokeCap.Round)
        fun circle(x: Float, y: Float, r: Float) =
            drawCircle(tint, r * w, Offset(x * w, y * w), style = Stroke(s))
        fun path(vararg points: Pair<Float, Float>) {
            val p = Path().apply {
                moveTo(points[0].first * w, points[0].second * w)
                points.drop(1).forEach { lineTo(it.first * w, it.second * w) }
            }
            drawPath(p, tint, style = Stroke(s, cap = StrokeCap.Round, join = StrokeJoin.Round))
        }
        when (name) {
            "back" -> path(.65f to .16f, .31f to .5f, .65f to .84f)
            "next" -> path(.38f to .24f, .65f to .5f, .38f to .76f)
            "add" -> { line(.5f, .16f, .5f, .84f); line(.16f, .5f, .84f, .5f) }
            "refresh" -> {
                drawArc(tint, 45f, 285f, false,
                    topLeft = Offset(.17f * w, .17f * w),
                    size = androidx.compose.ui.geometry.Size(.66f * w, .66f * w),
                    style = Stroke(s, cap = StrokeCap.Round))
                path(.59f to .29f, .78f to .31f, .77f to .49f)
            }
            "more" -> listOf(.22f, .5f, .78f).forEach { drawCircle(tint, .055f * w, Offset(.5f * w, it * w)) }
            "search" -> { circle(.43f, .43f, .27f); line(.64f, .64f, .88f, .88f) }
            "settings" -> {
                val gear = Path()
                repeat(8) { i ->
                    val a = i * Math.PI / 4
                    listOf(-.28 to .34, -.18 to .44, .18 to .44, .28 to .34).forEach { (delta, radius) ->
                        val x = (.5 + radius * cos(a + delta)).toFloat() * w
                        val y = (.5 + radius * sin(a + delta)).toFloat() * w
                        if (i == 0 && delta == -.28) gear.moveTo(x, y) else gear.lineTo(x, y)
                    }
                }
                gear.close()
                drawPath(gear, tint, style = Stroke(s, join = StrokeJoin.Round))
                circle(.5f, .5f, .15f)
            }
            "sun" -> {
                circle(.5f, .5f, .19f)
                repeat(8) { i ->
                    val a = i * Math.PI / 4
                    line((.5 + .33 * cos(a)).toFloat(), (.5 + .33 * sin(a)).toFloat(),
                        (.5 + .44 * cos(a)).toFloat(), (.5 + .44 * sin(a)).toFloat())
                }
            }
            "moon" -> {
                val p = Path().apply {
                    moveTo(.66f * w, .13f * w)
                    cubicTo(.16f * w, .23f * w, .22f * w, .83f * w, .7f * w, .87f * w)
                    cubicTo(.43f * w, 1.01f * w, .08f * w, .66f * w, .23f * w, .38f * w)
                    cubicTo(.31f * w, .2f * w, .48f * w, .13f * w, .66f * w, .13f * w)
                }
                drawPath(p, tint, style = Stroke(s, cap = StrokeCap.Round, join = StrokeJoin.Round))
            }
            "globe" -> {
                circle(.5f, .5f, .38f)
                drawOval(tint, topLeft = Offset(.35f * w, .12f * w),
                    size = androidx.compose.ui.geometry.Size(.3f * w, .76f * w), style = Stroke(s))
                line(.13f, .5f, .87f, .5f)
            }
            "person" -> { circle(.5f, .34f, .15f); drawArc(tint, 195f, 150f, false,
                topLeft = Offset(.2f * w, .48f * w),
                size = androidx.compose.ui.geometry.Size(.6f * w, .52f * w), style = Stroke(s)) }
            "room" -> { path(.18f to .85f, .18f to .21f, .82f to .21f, .82f to .85f);
                path(.42f to .85f, .42f to .48f, .68f to .48f, .68f to .85f) }
            "edit" -> { path(.17f to .83f, .24f to .59f, .68f to .15f, .85f to .32f, .41f to .76f, .17f to .83f);
                line(.59f, .24f, .76f, .41f) }
            "send" -> path(.16f to .82f, .86f to .5f, .16f to .18f, .3f to .49f, .86f to .5f)
        }
    }
}

@Composable
fun ChatHeader(title: String, onBack: (() -> Unit)? = null,
               onRefresh: (() -> Unit)? = null, onCreate: (() -> Unit)? = null,
               onSettings: (() -> Unit)? = null, onSearch: (() -> Unit)? = null,
               onMore: (() -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().height(64.dp), verticalAlignment = Alignment.CenterVertically) {
        if (onBack != null) {
            IconButton(onClick = onBack, modifier = Modifier.semantics { contentDescription = "Назад" }) {
                ChatIcon("back")
            }
        }
        Text(title, modifier = Modifier.weight(1f), fontSize = 22.sp,
            fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurface, maxLines = 1,
            overflow = TextOverflow.Ellipsis)
        if (onRefresh != null) {
            IconButton(onClick = onRefresh, modifier = Modifier.semantics { contentDescription = "Обновить" }) {
                ChatIcon("refresh")
            }
        }
        if (onCreate != null) {
            IconButton(onClick = onCreate, modifier = Modifier.semantics { contentDescription = "Создать диалог" }) {
                ChatIcon("add")
            }
        }
        if (onSettings != null) {
            IconButton(onClick = onSettings, modifier = Modifier.semantics { contentDescription = "Настройки" }) {
                ChatIcon("settings")
            }
        }
        if (onSearch != null) {
            IconButton(onClick = onSearch, modifier = Modifier.semantics { contentDescription = "Поиск сообщений" }) {
                ChatIcon("search")
            }
        }
        if (onMore != null) {
            IconButton(onClick = onMore, modifier = Modifier.semantics { contentDescription = "Меню комнаты" }) {
                ChatIcon("more")
            }
        }
    }
}

@Composable
fun ChatSettingsRow(icon: String, title: String, subtitle: String, onClick: (() -> Unit)? = null) {
    Column(Modifier.fillMaxWidth().padding(vertical = 4.dp)
        .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(14.dp))) {
        Row(Modifier.fillMaxWidth().heightIn(min = 72.dp)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(42.dp).background(MaterialTheme.colorScheme.primary.copy(alpha = 0.16f), CircleShape),
                contentAlignment = Alignment.Center) {
                ChatIcon(icon, tint = MaterialTheme.colorScheme.primary)
            }
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(title, fontSize = 16.sp, fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface)
                Text(subtitle, fontSize = 13.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1,
                    overflow = TextOverflow.Ellipsis)
            }
            if (onClick != null) ChatIcon("next", tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
fun ChatSection(value: String) {
    Text(value, modifier = Modifier.padding(start = 4.dp, top = 20.dp, bottom = 8.dp),
        fontSize = 14.sp, fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.primary)
}

@Composable
fun ChatText(value: String) {
    Text(value, modifier = Modifier.padding(horizontal = 4.dp, vertical = 6.dp),
        fontSize = 16.sp, color = MaterialTheme.colorScheme.onSurface)
}

@Composable
fun ChatButton(label: String, horizontal: Boolean, primary: Boolean, onClick: () -> Unit) {
    if (horizontal) {
        TextButton(onClick = onClick, modifier = Modifier.height(48.dp),
            colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.primary)) {
            Text(label, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    } else if (primary) {
        Button(onClick = onClick, modifier = Modifier.fillMaxWidth().padding(vertical = 3.dp)
            .heightIn(min = 48.dp), shape = RoundedCornerShape(10.dp),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary,
                contentColor = MaterialTheme.colorScheme.onPrimary)) {
            Text(label, fontSize = 14.sp)
        }
    } else {
        OutlinedButton(onClick = onClick, modifier = Modifier.fillMaxWidth().padding(vertical = 3.dp)
            .heightIn(min = 48.dp),
            shape = RoundedCornerShape(10.dp), contentPadding = PaddingValues(12.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.65f)),
            colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.primary)) {
            Text(label, fontSize = 14.sp)
        }
    }
}

@Composable
fun ChatRoomRow(name: String, onClick: () -> Unit) {
    Row(modifier = Modifier.fillMaxWidth().height(72.dp).clickable(onClick = onClick)
            .padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(modifier = Modifier.size(52.dp).background(MaterialTheme.colorScheme.primary, CircleShape),
                contentAlignment = Alignment.Center) {
                Text(name.firstOrNull()?.uppercase() ?: "#", color = MaterialTheme.colorScheme.onPrimary,
                    fontSize = 21.sp, fontWeight = FontWeight.Bold)
            }
            Spacer(Modifier.width(14.dp))
            Text(name, modifier = Modifier.weight(1f), fontSize = 16.sp,
                fontWeight = FontWeight.SemiBold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, color = MaterialTheme.colorScheme.onSurface)
    }
}

@Composable
fun ChatHeaderDivider() {
    Spacer(Modifier.fillMaxWidth().padding(top = 2.dp, bottom = 8.dp).height(1.dp)
        .background(MaterialTheme.colorScheme.outlineVariant))
}

@Composable
fun ChatComposer(value: String, enabled: Boolean, canSendText: Boolean, canAttach: Boolean,
                 onValueChange: (String) -> Unit, onAttach: () -> Unit, onSend: () -> Unit) {
    Row(modifier = Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.background).padding(8.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (canAttach) {
            val ink = MaterialTheme.colorScheme.onSurfaceVariant
            IconButton(onClick = onAttach, enabled = enabled,
                modifier = Modifier.semantics { contentDescription = "Прикрепить файл" }) {
                Canvas(Modifier.size(24.dp)) {
                    val stroke = 2.2.dp.toPx()
                    val w = size.width
                    val h = size.height
                    val clip = Path().apply {
                        moveTo(.70f * w, .32f * h)
                        lineTo(.37f * w, .68f * h)
                        cubicTo(.26f * w, .80f * h, .12f * w, .66f * h, .24f * w, .54f * h)
                        lineTo(.58f * w, .18f * h)
                        cubicTo(.79f * w, -.02f * h, 1.02f * w, .25f * h, .83f * w, .45f * h)
                        lineTo(.49f * w, .82f * h)
                        cubicTo(.29f * w, 1.03f * h, -.02f * w, .79f * h, .19f * w, .56f * h)
                    }
                    drawPath(clip, ink, style = Stroke(stroke, cap = StrokeCap.Round,
                        join = StrokeJoin.Round))
                }
            }
        }
        TextField(value = value, onValueChange = onValueChange, enabled = enabled && canSendText,
            label = { Text("Сообщение") },
            keyboardOptions = KeyboardOptions(autoCorrectEnabled = false),
            modifier = Modifier.weight(1f), shape = RoundedCornerShape(24.dp),
            maxLines = 4, colors = TextFieldDefaults.colors(
                focusedContainerColor = MaterialTheme.colorScheme.surfaceVariant,
                unfocusedContainerColor = MaterialTheme.colorScheme.surfaceVariant,
                focusedIndicatorColor = Color.Transparent,
                unfocusedIndicatorColor = Color.Transparent))
        if (canSendText) Button(onClick = onSend, enabled = enabled && value.isNotBlank(),
            modifier = Modifier.size(48.dp).semantics { contentDescription = "Отправить" }, shape = CircleShape,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary,
                contentColor = MaterialTheme.colorScheme.onPrimary)) {
            ChatIcon("send", tint = MaterialTheme.colorScheme.onPrimary)
        }
    }
}

@Composable
fun ChatThemeSelector(dark: Boolean, onChange: (Boolean) -> Unit) {
    Button(onClick = { onChange(!dark) }, modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp),
        shape = RoundedCornerShape(14.dp)) {
        ChatIcon(if (dark) "sun" else "moon", tint = MaterialTheme.colorScheme.onPrimary)
        Spacer(Modifier.width(12.dp))
        Text(if (dark) "Переключить на светлую тему" else "Переключить на тёмную тему",
            fontSize = 16.sp)
    }
}

@Composable
fun ManualActivationScreen(onBack: () -> Unit, onJson: (String) -> Unit,
                           onManual: (String, String, String, String, String, Boolean) -> Unit) {
    var json by rememberSaveable { mutableStateOf("") }
    var code by rememberSaveable { mutableStateOf("") }
    var firstUrl by rememberSaveable { mutableStateOf("") }
    var firstPin by rememberSaveable { mutableStateOf("") }
    var secondUrl by rememberSaveable { mutableStateOf("") }
    var secondPin by rememberSaveable { mutableStateOf("") }
    var secondIssuer by rememberSaveable { mutableIntStateOf(0) }
    Column(Modifier.fillMaxWidth()) {
        ChatHeader("Ручная настройка", onBack = onBack)
        ChatSection("Готовый JSON")
        Column(Modifier.fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(16.dp))
            .padding(14.dp)) {
        OutlinedTextField(json, { json = it }, modifier = Modifier.fillMaxWidth(),
            label = { Text("Данные активации JSON") },
            keyboardOptions = KeyboardOptions(autoCorrectEnabled = false), minLines = 4, maxLines = 8)
        Button(onClick = { onJson(json) }, enabled = json.isNotBlank(),
            modifier = Modifier.fillMaxWidth().padding(top = 12.dp).heightIn(min = 48.dp),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary,
                contentColor = MaterialTheme.colorScheme.onPrimary)) {
            Text("Подключиться по JSON")
        }
        }
        ChatSection("Или заполните поля")
        Column(Modifier.fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(16.dp))
            .padding(14.dp)) {
        ActivationField(code, { code = it }, "Одноразовый код")
        ActivationField(firstUrl, { firstUrl = it }, "Адрес узла 1 (https://…)")
        ActivationField(firstPin, { firstPin = it }, "Ключ узла 1 (tls_spki_sha256)")
        ActivationField(secondUrl, { secondUrl = it }, "Адрес узла 2 (https://…)")
        ActivationField(secondPin, { secondPin = it }, "Ключ узла 2 (tls_spki_sha256)")
        Text("Код выдал", modifier = Modifier.padding(top = 12.dp, bottom = 4.dp),
            color = MaterialTheme.colorScheme.onSurfaceVariant)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf("Узел 1", "Узел 2").forEachIndexed { index, label ->
                Box(Modifier.weight(1f).height(48.dp)
                    .background(if (secondIssuer == index) MaterialTheme.colorScheme.surfaceVariant
                        else Color.Transparent, RoundedCornerShape(12.dp))
                    .selectable(selected = secondIssuer == index, role = Role.RadioButton,
                        onClick = { secondIssuer = index }), contentAlignment = Alignment.Center) {
                    Text(label, color = if (secondIssuer == index) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
        Button(onClick = { onManual(code, firstUrl, firstPin, secondUrl, secondPin,
                secondIssuer == 1) },
            enabled = listOf(code, firstUrl, firstPin, secondUrl, secondPin).all { it.isNotBlank() },
            modifier = Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 24.dp)
                .heightIn(min = 48.dp),
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary,
                contentColor = MaterialTheme.colorScheme.onPrimary)) {
            Text("Подключиться вручную")
        }
        }
    }
}

@Composable
fun ChatModal(title: String, message: String? = null, options: List<String> = emptyList(),
              selectedIndex: Int? = null, confirmLabel: String? = null,
              dismissLabel: String = "Отмена", onOption: (Int) -> Unit = {},
              onConfirm: () -> Unit = {}, onDismiss: () -> Unit,
              body: @Composable () -> Unit = {}) {
    Surface(shape = RoundedCornerShape(28.dp), color = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().padding(24.dp)) {
            Text(title, fontSize = 21.sp, fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.padding(bottom = 14.dp))
            if (message != null) {
                Text(message, fontSize = 16.sp, color = MaterialTheme.colorScheme.onSurface,
                    modifier = Modifier.padding(bottom = 12.dp))
            }
            body()
            if (options.isNotEmpty()) {
                LazyColumn(Modifier.heightIn(max = 460.dp)) {
                    itemsIndexed(options) { index, label ->
                        Row(Modifier.fillMaxWidth().heightIn(min = 60.dp)
                            .clickable { onOption(index) }, verticalAlignment = Alignment.CenterVertically) {
                            if (selectedIndex != null) {
                                RadioButton(selected = index == selectedIndex,
                                    onClick = { onOption(index) })
                                Spacer(Modifier.width(10.dp))
                            }
                            Text(label, fontSize = 16.sp, color = MaterialTheme.colorScheme.onSurface)
                        }
                    }
                }
            }
            Row(Modifier.fillMaxWidth().padding(top = 12.dp),
                horizontalArrangement = Arrangement.End) {
                TextButton(onClick = onDismiss) {
                    Text(dismissLabel, color = MaterialTheme.colorScheme.primary)
                }
                if (confirmLabel != null) {
                    TextButton(onClick = onConfirm) {
                        Text(confirmLabel, color = MaterialTheme.colorScheme.primary)
                    }
                }
            }
        }
    }
}

@Composable
fun ChatRightsFields(values: List<Boolean>, onChange: (Int, Boolean) -> Unit) {
    listOf("Читать", "Отправлять сообщения", "Добавлять вложения", "Сохранять вложения")
        .forEachIndexed { index, label ->
            Row(Modifier.fillMaxWidth().heightIn(min = 56.dp)
                .clickable { onChange(index, !values[index]) },
                verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = values[index], onCheckedChange = { onChange(index, it) })
                Spacer(Modifier.width(8.dp))
                Text(label, color = MaterialTheme.colorScheme.onSurface)
            }
        }
}

@Composable
fun ChatMultiUserPicker(users: List<Pair<String, String>>, selected: List<String>,
                        onToggle: (String) -> Unit) {
    LazyColumn(Modifier.heightIn(max = 460.dp)) {
        itemsIndexed(users) { _, (id, name) ->
            Row(Modifier.fillMaxWidth().heightIn(min = 56.dp).clickable { onToggle(id) },
                verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = id in selected, onCheckedChange = { onToggle(id) })
                Spacer(Modifier.width(8.dp))
                Text(name, color = MaterialTheme.colorScheme.onSurface)
            }
        }
    }
}

@Composable
private fun ActivationField(value: String, onChange: (String) -> Unit, label: String) {
    OutlinedTextField(value, onChange, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
        label = { Text(label) }, singleLine = true,
        keyboardOptions = KeyboardOptions(autoCorrectEnabled = false))
}

@Preview(showBackground = true)
@Composable
private fun ChatListPreview() {
    ChatTheme {
        Column {
            ChatHeader("Чаты", onRefresh = {}, onCreate = {}, onSettings = {})
            ChatRoomRow("Общий чат") {}
            ChatRoomRow("Команда") {}
        }
    }
}

@Preview(showBackground = true, backgroundColor = 0xFF0D141B)
@Composable
private fun DarkChatListPreview() {
    ChatTheme(dark = true) {
        Column(Modifier.background(MaterialTheme.colorScheme.background)) {
            ChatHeader("Чаты", onRefresh = {}, onCreate = {}, onSettings = {})
            ChatThemeSelector(true) {}
            ChatRoomRow("Общий чат") {}
            ChatSettingsRow("settings", "Настройки", "Управление приложением") {}
        }
    }
}

@Preview(showBackground = true, backgroundColor = 0xFF0D141B)
@Composable
private fun ModalPreview() {
    ChatTheme {
        ChatModal("Выберите пользователя", options = listOf("Анна · Участник", "Иван · Капитан"),
            selectedIndex = 0, onDismiss = {})
    }
}
