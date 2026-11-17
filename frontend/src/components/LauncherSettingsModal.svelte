<script lang="ts">
  import { onMount } from 'svelte';
  import { Browser, Events } from '@wailsio/runtime';
  import { modalState } from '../modalState.svelte';
  import {
    GetLauncherSettings,
    SaveLauncherSettings,
    CheckLauncherUpdate,
    InstallLauncherUpdate,
    RestartLauncher,
    OpenLogsFolder
  } from '../../bindings/arclights/launcherservice.js';

  let {
    open = false,
    onClose = () => {}
  }: {
    open: boolean;
    onClose?: () => void;
  } = $props();

  let activeTab = $state<'launcher' | 'about'>('launcher');

  let autoLaunch = $state(false);
  let silentUpdate = $state(false);
  let closeAction = $state<'tray' | 'exit'>('exit');
  let speedLimitMode = $state<'none' | 'limit'>('none');
  let speedLimitKb = $state(4096);
  let selectedLanguage = $state('English');
  let updateChannel = $state<'release' | 'nightly'>('release');

  let checkingUpdate = $state(false);
  let currentVersion = $state();
  let updateAvailable = $state(false);
  let updateVersion = $state('');
  let updateDownloading = $state(false);
  let updateProgressPercent = $state(0);
  let updateReady = $state(false);

  let updateToast = $state<string | null>(null);
  let toastTimer: any = null;

  let closing = $state(false);
  let visible = $state(false);

  let modalX = $state(0);
  let modalY = $state(0);
  let isDragging = false;
  let startX = 0;
  let startY = 0;
  let initialModalX = 0;
  let initialModalY = 0;

  onMount(async () => {
    try {
      const info = await CheckLauncherUpdate();
      currentVersion = info?.current_version;
    } catch (err) {
      console.error('CheckLauncherUpdate failed:', err);
    }
  });

  function showToast(msg: string, duration = 3000) {
    updateToast = msg;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      updateToast = null;
    }, duration);
  }

  const CLOSE_DURATION = 250;

  $effect(() => {
    modalState.launcherSettingsOpen = open;
    if (open) {
      closing = false;
      visible = true;
      loadBackendSettings();
    } else {
      modalX = 0;
      modalY = 0;
    }
  });

  function handleClose() {
    if (closing) return;
    closing = true;
    setTimeout(() => {
      closing = false;
      visible = false;
      onClose();
    }, CLOSE_DURATION);
  }

  async function loadBackendSettings() {
    try {
      const cfg = await GetLauncherSettings();
      if (cfg) {
        autoLaunch = Boolean(cfg.auto_launch);
        silentUpdate = Boolean(cfg.silent_update);
        closeAction = cfg.close_action === 'tray' ? 'tray' : 'exit';
        speedLimitMode = cfg.speed_limit_mode === 'limit' ? 'limit' : 'none';
        speedLimitKb = cfg.speed_limit_kb > 0 ? cfg.speed_limit_kb : 4096;
        selectedLanguage = cfg.language || 'English';
        updateChannel = cfg.update_channel === 'nightly' ? 'nightly' : 'release';

        localStorage.setItem('arclights_close_action', closeAction);
      }
    } catch (e) {
      console.warn('Failed to load settings:', e);
    }
  }

  async function persistSettings() {
    try {
      localStorage.setItem('arclights_close_action', closeAction);
      localStorage.setItem('arclights_auto_launch', String(autoLaunch));
      localStorage.setItem('arclights_silent_update', String(silentUpdate));

      const current = await GetLauncherSettings();
      current.auto_launch = autoLaunch;
      current.silent_update = silentUpdate;
      current.close_action = closeAction;
      current.speed_limit_mode = speedLimitMode;
      current.speed_limit_kb = speedLimitKb;
      current.language = selectedLanguage;
      current.update_channel = updateChannel;

      await SaveLauncherSettings(current);
    } catch (e) {
      console.error('Failed to save settings:', e);
    }
  }

  function handleAutoLaunchToggle() {
    autoLaunch = !autoLaunch;
    persistSettings();
  }

  function handleSilentUpdateToggle() {
    silentUpdate = !silentUpdate;
    persistSettings();
  }

  function handleCloseActionChange(act: 'tray' | 'exit') {
    closeAction = act;
    persistSettings();
  }

  function handleSpeedLimitModeChange(mode: 'none' | 'limit') {
    speedLimitMode = mode;
    persistSettings();
  }

  function handleSpeedLimitInputChange(e: Event) {
    const val = Math.max(100, Math.min(20480, Number((e.target as HTMLInputElement).value) || 4096));
    speedLimitKb = val;
    persistSettings();
  }

  function handleChannelChange(chan: 'release' | 'nightly') {
    updateChannel = chan;
    persistSettings();
  }

  async function handleCheckUpdates() {
    if (checkingUpdate || updateDownloading) return;
    checkingUpdate = true;
    showToast('Checking for updates...');

    try {
      const info = await CheckLauncherUpdate();
      checkingUpdate = false;
      if (info && info.has_update) {
        updateAvailable = true;
        updateVersion = info.latest_version;
        showToast(`Update found: ${info.latest_version}! Starting download...`, 4000);
        updateDownloading = true;
        try {
          await InstallLauncherUpdate();
        } catch (e) {
          console.warn('Download & install error:', e);
          updateDownloading = false;
        }
      } else {
        updateAvailable = false;
        showToast(`You are already using the latest version (${info?.current_version})`);
      }
    } catch {
      checkingUpdate = false;
      showToast('You are already using the latest version');
    }
  }

  async function handleRestartNow() {
    try {
      await RestartLauncher();
    } catch (e) {
      console.error('Failed to restart:', e);
    }
  }

  async function handleOpenLogs() {
    try {
      await OpenLogsFolder();
    } catch (e) {
      console.error('Failed to open logs:', e);
    }
  }

  function openTermsOfService() {
    Browser.OpenURL('https://user.gryphline.com/en-us/protocol/plain/launcher/terms_of_service');
  }

  function openPrivacyPolicy() {
    Browser.OpenURL('https://user.gryphline.com/en-us/protocol/plain/launcher/privacy_policy');
  }

  $effect(() => {
    const unsubProgress = Events.On('wails:updater:download-progress', (e: any) => {
      if (e?.data?.[0]) {
        const prog = e.data[0];
        if (prog.total > 0) {
          updateProgressPercent = Math.round((prog.downloaded / prog.total) * 100);
        }
      }
    });

    const unsubReady = Events.On('wails:updater:update-ready', () => {
      updateDownloading = false;
      updateReady = true;
      showToast('Update ready to install! Click "Restart launcher" to apply.', 6000);
    });

    const unsubError = Events.On('wails:updater:error', (e: any) => {
      updateDownloading = false;
      console.warn('Updater event:', e);
    });

    return () => {
      unsubProgress();
      unsubReady();
      unsubError();
    };
  });

  function handleWindowMouseDown(e: MouseEvent) {
    if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .checkbox-row, .radio-row, .select-box')) {
      return;
    }
    isDragging = true;
    startX = e.clientX;
    startY = e.clientY;
    initialModalX = modalX;
    initialModalY = modalY;

    window.addEventListener('mousemove', handleWindowMouseMove);
    window.addEventListener('mouseup', handleWindowMouseUp);
  }

  function handleWindowMouseMove(e: MouseEvent) {
    if (!isDragging) return;
    modalX = initialModalX + (e.clientX - startX);
    modalY = initialModalY + (e.clientY - startY);
  }

  function handleWindowMouseUp() {
    isDragging = false;
    window.removeEventListener('mousemove', handleWindowMouseMove);
    window.removeEventListener('mouseup', handleWindowMouseUp);
  }
</script>

{#if open || visible}
  <div class="modal-backdrop launcher-modal-backdrop" class:closing data-no-drag>
    {#if updateToast}
      <div class="toast-popup" role="status">
        {updateToast}
      </div>
    {/if}

    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="modal-window launcher-modal-window"
      class:closing
      style="--drag-x: {modalX}px; --drag-y: {modalY}px; transform: translate(var(--drag-x, 0px), var(--drag-y, 0px));"
      role="dialog"
      aria-modal="true"
      aria-labelledby="launcher-settings-title"
      tabindex="-1"
      onmousedown={handleWindowMouseDown}
    >
      <aside class="tabs-sidebar">
        <div class="sidebar-header" id="launcher-settings-title">
          <svg class="title-icon" width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path fill-rule="evenodd" clip-rule="evenodd" d="M4.36128 3.22889L3.61641 2.48401L2.2022 3.89823L2.99448 4.69051C2.50552 5.42858 2.17543 6.28107 2.05297 7.19922L0.5 7.19922V9.19922H2.11986C2.30316 10.1028 2.68953 10.9327 3.22926 11.6392L2.48445 12.384L3.89866 13.7982L4.69104 13.0059C5.42898 13.4946 6.28128 13.8246 7.19922 13.947V15.5H9.19921L9.19922 13.8801C9.96581 13.7246 10.6794 13.423 11.3095 13.0055L12.1017 13.7977L13.5159 12.3835L12.7711 11.6387C13.3106 10.9323 13.6969 10.1026 13.8801 9.19922H15.5V7.19922H13.947C13.8246 6.28129 13.4946 5.42899 13.0059 4.69104L13.7982 3.89875L12.3839 2.48453L11.6392 3.22927C10.9327 2.68953 10.1028 2.30316 9.19922 2.11986L9.19922 0.5H7.19922V2.05297C6.14117 2.19408 5.17032 2.61093 4.36128 3.22889ZM8 12.4C10.4301 12.4 12.4 10.4301 12.4 8C12.4 5.56995 10.4301 3.6 8 3.6C5.56995 3.6 3.6 5.56995 3.6 8C3.6 10.4301 5.56995 12.4 8 12.4ZM8 10C9.10457 10 10 9.10457 10 8C10 6.89543 9.10457 6 8 6C6.89543 6 6 6.89543 6 8C6 9.10457 6.89543 10 8 10Z" fill="white"/>
          </svg>
          <span>Settings</span>
        </div>

        <nav class="tab-list">
          <button
            type="button"
            class="tab-btn"
            class:active={activeTab === 'launcher'}
            onclick={() => (activeTab = 'launcher')}
          >
            Launcher Settings
          </button>
          <button
            type="button"
            class="tab-btn"
            class:active={activeTab === 'about'}
            onclick={() => (activeTab = 'about')}
          >
            About
          </button>
        </nav>
      </aside>

      <div class="right-col">
        <div class="top-bar">
          <button type="button" class="close-btn" aria-label="Close" onclick={handleClose}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
              <rect x="12" y="11.2928" width="1" height="1" transform="rotate(45 12 11.2928)" stroke="currentColor"/>
              <path fill-rule="evenodd" clip-rule="evenodd" d="M13.5562 9.31353L17.0917 5.778L18.5059 7.19221L14.9704 10.7277L13.5562 9.31353ZM5.77746 17.0912L9.31299 13.5556L10.7272 14.9698L7.19167 18.5054L5.77746 17.0912ZM14.687 13.5556L18.2225 17.0912L16.8083 18.5054L13.2728 14.9698L14.687 13.5556ZM6.90883 5.77745L10.4444 9.31298L9.03015 10.7272L5.49462 7.19166L6.90883 5.77745Z" fill="currentColor"/>
            </svg>
          </button>
        </div>

        <main class="content-panel">
          {#if activeTab === 'launcher'}
            <div class="setting-card">
              <h3 class="card-heading">Language Settings</h3>
              <div class="card-divider"></div>
              <div class="select-box">
                <span>{selectedLanguage}</span>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="m6 9 6 6 6-6"/>
                </svg>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Launch Settings</h3>
              <div class="card-divider"></div>
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div class="checkbox-row" onclick={handleAutoLaunchToggle}>
                <div class="custom-checkbox" class:checked={autoLaunch}>
                  {#if autoLaunch}
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="#18171A" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  {/if}
                </div>
                <div class="checkbox-texts">
                  <span class="checkbox-label">Auto-launch at startup</span>
                  <span class="checkbox-subtext">Open the launcher automatically at startup.</span>
                </div>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Update Settings</h3>
              <div class="card-divider"></div>
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div class="checkbox-row" onclick={handleSilentUpdateToggle}>
                <div class="custom-checkbox" class:checked={silentUpdate}>
                  {#if silentUpdate}
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="#18171A" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  {/if}
                </div>
                <div class="checkbox-texts">
                  <span class="checkbox-label">Enable Silent Updates</span>
                  <span class="checkbox-subtext">Allow the launcher to automatically download and install launcher updates, then prompt for a restart when finished.</span>
                </div>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Update Channel</h3>
              <div class="card-divider"></div>
              <div class="radio-group">
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleChannelChange('release')}>
                  <div class="custom-radio" class:checked={updateChannel === 'release'}>
                    {#if updateChannel === 'release'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <span class="radio-label">Release</span>
                </div>

                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleChannelChange('nightly')}>
                  <div class="custom-radio" class:checked={updateChannel === 'nightly'}>
                    {#if updateChannel === 'nightly'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <span class="radio-label">Nightly</span>
                </div>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">When Closing Window</h3>
              <div class="card-divider"></div>
              <div class="radio-group">
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleCloseActionChange('tray')}>
                  <div class="custom-radio" class:checked={closeAction === 'tray'}>
                    {#if closeAction === 'tray'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <span class="radio-label">Minimize to tray</span>
                </div>

                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleCloseActionChange('exit')}>
                  <div class="custom-radio" class:checked={closeAction === 'exit'}>
                    {#if closeAction === 'exit'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <span class="radio-label">Exit launcher</span>
                </div>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Download Speed Limit</h3>
              <div class="card-divider"></div>
              <div class="radio-group">
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleSpeedLimitModeChange('none')}>
                  <div class="custom-radio" class:checked={speedLimitMode === 'none'}>
                    {#if speedLimitMode === 'none'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <span class="radio-label">No limit</span>
                </div>

                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="radio-row" onclick={() => handleSpeedLimitModeChange('limit')}>
                  <div class="custom-radio" class:checked={speedLimitMode === 'limit'}>
                    {#if speedLimitMode === 'limit'}
                      <div class="radio-dot"></div>
                    {/if}
                  </div>
                  <div class="speed-input-wrap" onclick={(e) => e.stopPropagation()}>
                    <input
                      type="number"
                      class="speed-input"
                      value={speedLimitKb}
                      min="100"
                      max="20480"
                      onchange={handleSpeedLimitInputChange}
                    />
                    <span class="speed-unit">KB/S (100~20480)</span>
                  </div>
                </div>
              </div>
            </div>

          {:else}
            <div class="setting-card">
              <div class="card-header-row">
                <h3 class="card-heading">Launcher Version</h3>
                <span class="version-text">Current Version: {currentVersion}</span>
              </div>
              <div class="card-divider"></div>

              {#if updateReady}
                <button type="button" class="action-row update-ready-row" onclick={handleRestartNow}>
                  <span class="update-ready-label">Update Ready! Restart Launcher</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/>
                  </svg>
                </button>
              {:else if updateDownloading}
                <div class="downloading-row">
                  <div class="dl-info">
                    <span>Downloading update ({updateVersion})...</span>
                    <span>{updateProgressPercent}%</span>
                  </div>
                  <div class="dl-bar-bg">
                    <div class="dl-bar-fill" style="width: {updateProgressPercent}%;"></div>
                  </div>
                </div>
              {:else}
                <button type="button" class="action-row" onclick={handleCheckUpdates} disabled={checkingUpdate}>
                  <span>{checkingUpdate ? 'Checking for updates...' : 'Check for updates'}</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                </button>
              {/if}
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Client Log</h3>
              <div class="card-divider"></div>
              <div class="action-list">
                <button type="button" class="action-row" onclick={handleOpenLogs}>
                  <span>View</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                </button>
                <button type="button" class="action-row" onclick={() => showToast('Login required to submit logs')}>
                  <span>Submit Logs (Login Required)</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                </button>
              </div>
            </div>

            <div class="setting-card">
              <h3 class="card-heading">Launcher Service Agreements</h3>
              <div class="card-divider"></div>
              <div class="action-list">
                <button type="button" class="action-row" onclick={openTermsOfService}>
                  <span>Game Launcher Terms of Service</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                </button>
                <button type="button" class="action-row" onclick={openPrivacyPolicy}>
                  <span>Game Launcher Privacy Policy</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                </button>
              </div>
            </div>
          {/if}
        </main>
      </div>
    </div>
  </div>
{/if}
