package ru.rassvet.chat

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import java.io.File

class ShareProvider : ContentProvider() {
    override fun onCreate() = true

    private fun file(uri: Uri): File {
        val name = uri.lastPathSegment ?: error("Неверный файл")
        require(name.matches(Regex("[0-9]{13}-[0-9a-f-]{36}"))) { "Неверный файл" }
        val result = File(File(context!!.filesDir, "shares"), name)
        if (name.substringBefore('-').toLong() <= System.currentTimeMillis()) {
            result.delete()
            error("Срок доступа истёк")
        }
        return result
    }

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        require(mode == "r") { "Только чтение" }
        return ParcelFileDescriptor.open(file(uri), ParcelFileDescriptor.MODE_READ_ONLY)
    }

    override fun getType(uri: Uri): String = uri.getQueryParameter("type") ?: "application/octet-stream"

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?,
                       selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val value = file(uri)
        require(value.isFile) { "Файл не найден" }
        val columns = projection ?: arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        return MatrixCursor(columns).apply {
            addRow(columns.map {
                when (it) {
                    OpenableColumns.DISPLAY_NAME -> uri.getQueryParameter("name") ?: "file"
                    OpenableColumns.SIZE -> value.length()
                    else -> null
                }
            })
        }
    }

    override fun insert(uri: Uri, values: ContentValues?): Uri? = throw UnsupportedOperationException()
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
    override fun update(uri: Uri, values: ContentValues?, selection: String?,
                        selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException()
}
