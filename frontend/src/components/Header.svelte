<script lang="ts">
  import { Window } from '@wailsio/runtime';
  import { modalState } from '../modalState.svelte';
  import LauncherSettingsModal from './LauncherSettingsModal.svelte';

  let {
    children
  }: {
    children?: any;
  } = $props();

  let isSettingsOpen = $state(false);
  let hoveredBtn = $state<'settings' | 'minimize' | 'close' | null>(null);

  function openSettings() {
    isSettingsOpen = true;
  }

  function handleMinimize() {
    try {
      Window.Minimise();
    } catch (e) {
      console.error('Failed to minimize window:', e);
    }
  }

  function handleClose() {
    try {
      const closeAction = localStorage.getItem('arclights_close_action');
      if (closeAction === 'tray') {
        Window.Hide();
      } else {
        Window.Close();
      }
    } catch (e) {
      console.error('Failed to close window:', e);
    }
  }
</script>

<header class="app-header header-drag-region" class:no-drag={modalState.isAnyOpen}>
  <div class="header-left">
    {#if children}
      {@render children()}
    {/if}
  </div>

  <div class="header-controls" data-no-drag>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="btn-wrapper"
      onmouseenter={() => (hoveredBtn = 'settings')}
      onmouseleave={() => (hoveredBtn = null)}
    >
      <button
        type="button"
        class="header-icon"
        aria-label="Settings"
        onclick={openSettings}
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
          <path fill-rule="evenodd" clip-rule="evenodd" d="M4.36128 3.22889L3.61641 2.48401L2.2022 3.89823L2.99448 4.69051C2.50552 5.42858 2.17543 6.28107 2.05297 7.19922L0.5 7.19922V9.19922H2.11986C2.30316 10.1028 2.68953 10.9327 3.22926 11.6392L2.48445 12.384L3.89866 13.7982L4.69104 13.0059C5.42898 13.4946 6.28128 13.8246 7.19922 13.947V15.5H9.19921L9.19922 13.8801C9.96581 13.7246 10.6794 13.423 11.3095 13.0055L12.1017 13.7977L13.5159 12.3835L12.7711 11.6387C13.3106 10.9323 13.6969 10.1026 13.8801 9.19922H15.5V7.19922H13.947C13.8246 6.28129 13.4946 5.42899 13.0059 4.69104L13.7982 3.89875L12.3839 2.48453L11.6392 3.22927C10.9327 2.68953 10.1028 2.30316 9.19922 2.11986L9.19922 0.5H7.19922V2.05297C6.14117 2.19408 5.17032 2.61093 4.36128 3.22889ZM8 12.4C10.4301 12.4 12.4 10.4301 12.4 8C12.4 5.56995 10.4301 3.6 8 3.6C5.56995 3.6 3.6 5.56995 3.6 8C3.6 10.4301 5.56995 12.4 8 12.4ZM8 10C9.10457 10 10 9.10457 10 8C10 6.89543 9.10457 6 8 6C6.89543 6 6 6.89543 6 8C6 9.10457 6.89543 10 8 10Z" fill="white"/>
        </svg>
      </button>

      {#if hoveredBtn === 'settings'}
        <div class="tooltip-bubble" role="tooltip">
          <div class="bubble-arrow"></div>
          <span class="bubble-text">Settings</span>
        </div>
      {/if}
    </div>

    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="btn-wrapper"
      onmouseenter={() => (hoveredBtn = 'minimize')}
      onmouseleave={() => (hoveredBtn = null)}
    >
      <button
        type="button"
        class="header-icon"
        aria-label="Minimize"
        onclick={handleMinimize}
      >
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <line y1="6.80005" x2="14" y2="6.80005" stroke="white" stroke-width="2"/>
        </svg>
      </button>

      {#if hoveredBtn === 'minimize'}
        <div class="tooltip-bubble" role="tooltip">
          <div class="bubble-arrow"></div>
          <span class="bubble-text">Minimize</span>
        </div>
      {/if}
    </div>

    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="btn-wrapper"
      onmouseenter={() => (hoveredBtn = 'close')}
      onmouseleave={() => (hoveredBtn = null)}
    >
      <button
        type="button"
        class="header-icon"
        aria-label="Close"
        onclick={handleClose}
      >
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none">
          <rect x="12" y="11.2928" width="1" height="1" transform="rotate(45 12 11.2928)" stroke="white"/>
          <path fill-rule="evenodd" clip-rule="evenodd" d="M13.5562 9.31353L17.0917 5.778L18.5059 7.19221L14.9704 10.7277L13.5562 9.31353ZM5.77746 17.0912L9.31299 13.5556L10.7272 14.9698L7.19167 18.5054L5.77746 17.0912ZM14.687 13.5556L18.2225 17.0912L16.8083 18.5054L13.2728 14.9698L14.687 13.5556ZM6.90883 5.77745L10.4444 9.31298L9.03015 10.7272L5.49462 7.19166L6.90883 5.77745Z" fill="white"/>
        </svg>
      </button>

      {#if hoveredBtn === 'close'}
        <div class="tooltip-bubble" role="tooltip">
          <div class="bubble-arrow"></div>
          <span class="bubble-text">Close</span>
        </div>
      {/if}
    </div>
  </div>
</header>

<LauncherSettingsModal
  open={isSettingsOpen}
  onClose={() => (isSettingsOpen = false)}
/>
