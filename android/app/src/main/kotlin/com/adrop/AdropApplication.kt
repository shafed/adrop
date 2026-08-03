package com.adrop

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import android.util.Log
import com.adrop.data.trust.TrustRepository
import com.adrop.feature.fcm.FcmTokenStore
import com.adrop.feature.receive.ReceiveForegroundService
import com.adrop.feature.send.SendWorker
import com.adrop.net.mdns.MdnsManager
import com.google.firebase.messaging.FirebaseMessaging

/**
 * Application entry-point. Creates notification channels on first run.
 */
class AdropApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        createNotificationChannels()
        startBackgroundDiscovery()
        fetchFcmToken()
        // Flush any sends that were queued while a PC was offline. Gated on
        // network connectivity by the worker itself; a no-op if the queue is empty.
        SendWorker.enqueueAll(this)
    }

    private fun startBackgroundDiscovery() {
        val trustRepo = TrustRepository.getInstance(this)
        MdnsManager(this, trustRepo).startDiscovery()
    }

    // AdropFirebaseService.onNewToken only fires when a token is (re)issued,
    // which may not happen again on a plain app relaunch. Without this, a
    // device that missed that event never gets a cached token to send in
    // Hello, and the PC can never FCM-wake it.
    private fun fetchFcmToken() {
        FirebaseMessaging.getInstance().token.addOnCompleteListener { task ->
            if (task.isSuccessful) {
                FcmTokenStore.save(this, task.result)
            } else {
                Log.w("AdropApplication", "FCM token fetch failed", task.exception)
            }
        }
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val nm = getSystemService(NotificationManager::class.java)

        nm.createNotificationChannel(
            NotificationChannel(
                ReceiveForegroundService.CHANNEL_SERVICE,
                "Receive window",
                NotificationManager.IMPORTANCE_LOW,
            ).apply { description = "Shown while the receive window is open" }
        )

        nm.createNotificationChannel(
            NotificationChannel(
                CHANNEL_TRANSFERS,
                "Transfers",
                NotificationManager.IMPORTANCE_DEFAULT,
            ).apply { description = "Completed file and clipboard transfers" }
        )
    }

    companion object {
        const val CHANNEL_TRANSFERS = "adrop_transfers"
    }
}
