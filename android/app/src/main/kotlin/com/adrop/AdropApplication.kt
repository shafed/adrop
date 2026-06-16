package com.adrop

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import com.adrop.feature.receive.ReceiveForegroundService

/**
 * Application entry-point. Creates notification channels on first run.
 */
class AdropApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        createNotificationChannels()
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
