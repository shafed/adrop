package com.adrop.feature.fcm

import android.content.Context
import android.content.Intent
import android.util.Log
import com.adrop.feature.receive.ReceiveForegroundService
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

class AdropFirebaseService : FirebaseMessagingService() {

    override fun onNewToken(token: String) {
        Log.d(TAG, "FCM token refreshed")
        FcmTokenStore.save(applicationContext, token)
    }

    override fun onMessageReceived(message: RemoteMessage) {
        val type = message.data["type"] ?: return
        if (type != "wake") return
        val sender = message.data["sender"] ?: "unknown"
        Log.i(TAG, "FCM wake from $sender — opening receive window")
        val intent = ReceiveForegroundService.startIntent(applicationContext)
            .putExtra(EXTRA_WAKE_SENDER, sender)
        startForegroundService(intent)
    }

    companion object {
        private const val TAG = "AdropFCM"
        const val EXTRA_WAKE_SENDER = "wake_sender"
    }
}

object FcmTokenStore {
    private const val PREFS = "adrop_fcm"
    private const val KEY_TOKEN = "token"

    fun save(context: Context, token: String) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit().putString(KEY_TOKEN, token).apply()
    }

    fun load(context: Context): String? =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY_TOKEN, null)
}
