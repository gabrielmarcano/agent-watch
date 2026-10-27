package com.gabriel.agentwatch.data

import android.content.SharedPreferences

interface KeyValueStore {
    fun getString(key: String, default: String? = null): String?
    fun putString(key: String, value: String?)
    fun getBoolean(key: String, default: Boolean = false): Boolean
    fun putBoolean(key: String, value: Boolean)
    fun getLong(key: String, default: Long = 0L): Long
    fun putLong(key: String, value: Long)
    fun getStringSet(key: String): Set<String>
    fun putStringSet(key: String, values: Set<String>)
    fun remove(key: String)
    fun clear()
    fun contains(key: String): Boolean
    fun allKeys(): Set<String>
}

class SharedPrefsKeyValueStore(private val prefs: SharedPreferences) : KeyValueStore {
    override fun getString(key: String, default: String?): String? =
        prefs.getString(key, default)

    override fun putString(key: String, value: String?) {
        prefs.edit().apply {
            if (value == null) remove(key) else putString(key, value)
        }.apply()
    }

    override fun getBoolean(key: String, default: Boolean): Boolean =
        prefs.getBoolean(key, default)

    override fun putBoolean(key: String, value: Boolean) {
        prefs.edit().putBoolean(key, value).apply()
    }

    override fun getLong(key: String, default: Long): Long =
        prefs.getLong(key, default)

    override fun putLong(key: String, value: Long) {
        prefs.edit().putLong(key, value).apply()
    }

    override fun getStringSet(key: String): Set<String> =
        prefs.getStringSet(key, null)?.toSet() ?: emptySet()

    override fun putStringSet(key: String, values: Set<String>) {
        prefs.edit().putStringSet(key, values.toSet()).apply()
    }

    override fun remove(key: String) {
        prefs.edit().remove(key).apply()
    }

    override fun clear() {
        prefs.edit().clear().apply()
    }

    override fun contains(key: String): Boolean =
        prefs.contains(key)

    override fun allKeys(): Set<String> =
        prefs.all.keys.toSet()
}

class InMemoryKeyValueStore : KeyValueStore {
    private val lock = Any()
    private val data = mutableMapOf<String, Any>()

    override fun getString(key: String, default: String?): String? = synchronized(lock) {
        data[key] as? String ?: default
    }

    override fun putString(key: String, value: String?) {
        synchronized(lock) {
            if (value == null) data.remove(key) else data[key] = value
        }
    }

    override fun getBoolean(key: String, default: Boolean): Boolean = synchronized(lock) {
        data[key] as? Boolean ?: default
    }

    override fun putBoolean(key: String, value: Boolean) {
        synchronized(lock) {
            data[key] = value
        }
    }

    override fun getLong(key: String, default: Long): Long = synchronized(lock) {
        (data[key] as? Number)?.toLong() ?: default
    }

    override fun putLong(key: String, value: Long) {
        synchronized(lock) {
            data[key] = value
        }
    }

    @Suppress("UNCHECKED_CAST")
    override fun getStringSet(key: String): Set<String> = synchronized(lock) {
        (data[key] as? Set<String>)?.toSet() ?: emptySet()
    }

    override fun putStringSet(key: String, values: Set<String>) {
        synchronized(lock) {
            data[key] = values.toSet()
        }
    }

    override fun remove(key: String) {
        synchronized(lock) {
            data.remove(key)
        }
    }

    override fun clear() {
        synchronized(lock) {
            data.clear()
        }
    }

    override fun contains(key: String): Boolean = synchronized(lock) {
        data.containsKey(key)
    }

    override fun allKeys(): Set<String> = synchronized(lock) {
        data.keys.toSet()
    }
}
