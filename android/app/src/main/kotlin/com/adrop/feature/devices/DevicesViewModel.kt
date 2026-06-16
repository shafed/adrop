package com.adrop.feature.devices

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.adrop.data.trust.TrustedDevice
import com.adrop.data.trust.TrustRepository
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class DevicesViewModel(private val repo: TrustRepository) : ViewModel() {

    val devices: StateFlow<List<TrustedDevice>> = repo.devicesFlow
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    fun revoke(device: TrustedDevice) {
        viewModelScope.launch { repo.remove(device.id) }
    }

    companion object {
        fun factory(context: Context) = object : ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : ViewModel> create(modelClass: Class<T>): T =
                DevicesViewModel(TrustRepository.getInstance(context)) as T
        }
    }
}
