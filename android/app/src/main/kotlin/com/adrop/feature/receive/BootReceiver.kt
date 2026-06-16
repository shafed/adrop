package com.adrop.feature.receive

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Boot receiver — placeholder for future FCM wake-on-boot functionality.
 * Currently does nothing but is declared in the manifest so the app can
 * receive BOOT_COMPLETED when needed (Phase 2).
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        // Phase 2: could pre-open a short receive window or re-register FCM token.
    }
}
