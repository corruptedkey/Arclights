<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import { LauncherService } from '../../bindings/arclights';
  import type { GameStateInfo, DiskSpaceInfo } from '../../bindings/arclights/models';
  import InstallModal from './InstallModal.svelte';
  import SettingsModal from './SettingsModal.svelte';
  import { modalState } from '../modalState.svelte';

  let gameState = $state<GameStateInfo | null>(null);
  let status = $state<string>('not_installed');
  let installPath = $state<string>('');
  let createShortcut = $state<boolean>(true);
  let diskSpace = $state<DiskSpaceInfo | null>(null);

  let downloadPercent = $state<number>(0);
  let downloadSpeed = $state<number>(0);
  let downloadedBytes = $state<number>(0);
  let totalDownloadBytes = $state<number>(62410385966);
  let verifyPercent = $state<number>(0);
  let errorMessage = $state<string>('');

  let isButtonHovered = $state<boolean>(false);
  let isCardHovered = $state<boolean>(false);
  let isArrowAnimating = $state<boolean>(false);
  let isPressed = $state<boolean>(false);

  let isMenuOpen = $state<boolean>(false);
  let isModalOpen = $state<boolean>(false);
  let isSettingsOpen = $state<boolean>(false);
  let toastMessage = $state<string | null>(null);
  let toastTimer: any = null;

  $effect(() => {
    modalState.installOpen = isModalOpen;
    modalState.gameSettingsOpen = isSettingsOpen;
  });

  let unsubEvents: (() => void)[] = [];
  let menuContainer: HTMLDivElement | undefined = $state();

  function showToast(msg: string) {
    toastMessage = msg;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      toastMessage = null;
    }, 3000);
  }

  function formatBytes(bytes: number): string {
    if (!bytes || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let val = bytes;
    let unitIndex = 0;
    while (val >= 1024 && unitIndex < units.length - 1) {
      val /= 1024;
      unitIndex++;
    }
    return `${val >= 100 || unitIndex === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[unitIndex]}`;
  }

  function formatSpeed(bps?: number): string {
    if (!bps || bps <= 0) return '0 MB/s';
    if (bps >= 1024 * 1024) {
      return (bps / (1024 * 1024)).toFixed(1) + ' MB/s';
    }
    return (bps / 1024).toFixed(0) + ' KB/s';
  }

  function formatEta(seconds?: number): string {
    if (seconds === undefined || seconds === null || seconds < 0 || !isFinite(seconds)) {
      return '--:--:--';
    }
    const h = Math.floor(seconds / 3600).toString().padStart(2, '0');
    const m = Math.floor((seconds % 3600) / 60).toString().padStart(2, '0');
    const s = Math.floor(seconds % 60).toString().padStart(2, '0');
    return `${h}:${m}:${s}`;
  }

  async function refreshGameState() {
    try {
      const state = await LauncherService.GetGameState();
      if (state) {
        gameState = state;
        status = state.status;
        installPath = state.install_path;
        createShortcut = state.create_shortcut;
        diskSpace = state.disk_space;
        if (diskSpace && diskSpace.required_bytes) {
          totalDownloadBytes = diskSpace.required_bytes;
        }
        if (state.status === 'not_installed') {
          downloadPercent = 0;
          downloadSpeed = 0;
          downloadedBytes = 0;
        }
      }
    } catch (e) {
      console.error('Failed to get game state:', e);
    }
  }

  onMount(() => {
    refreshGameState();

    const unsubStatus = Events.On('installer:status:change', (ev) => {
      if (ev.data) {
        status = String(ev.data);
        if (status === 'ready' || status === 'paused' || status === 'not_installed' || status === 'running' || status === 'booting') {
          refreshGameState();
        }
      }
    });

    const unsubProgress = Events.On('installer:stream:progress', (ev) => {
      if (ev.data) {
        status = 'downloading';
        downloadPercent = ev.data.percent ?? 0;
        downloadSpeed = ev.data.speed_bps ?? 0;
        downloadedBytes = ev.data.downloaded_bytes ?? 0;
        if (ev.data.total_download_bytes) {
          totalDownloadBytes = ev.data.total_download_bytes;
        }
      }
    });

    const unsubVerify = Events.On('installer:verify:progress', (ev) => {
      if (ev.data) {
        status = 'verifying';
        verifyPercent = ev.data.percent ?? 0;
      }
    });

    const unsubError = Events.On('installer:error', (ev) => {
      if (ev.data) {
        status = 'error';
        errorMessage = String(ev.data);
        showToast(errorMessage);
      }
    });

    unsubEvents = [unsubStatus, unsubProgress, unsubVerify, unsubError];

    function handleClickOutside(event: MouseEvent) {
      if (isMenuOpen && menuContainer && !menuContainer.contains(event.target as Node)) {
        isMenuOpen = false;
      }
    }
    window.addEventListener('click', handleClickOutside);

    return () => {
      window.removeEventListener('click', handleClickOutside);
    };
  });

  onDestroy(() => {
    unsubEvents.forEach((u) => u());
    if (toastTimer) clearTimeout(toastTimer);
  });

  function triggerArrowAnimation() {
    isArrowAnimating = true;
  }

  function handleArrowAnimationEnd() {
    isArrowAnimating = false;
  }

  async function handleMainAction() {
    if (status === 'running' || status === 'booting') {
      return;
    }

    isMenuOpen = false;
    triggerArrowAnimation();

    if (status === 'not_installed') {
      isModalOpen = true;
    } else if (status === 'downloading') {
      try {
        await LauncherService.CancelOperation();
        status = 'paused';
      } catch (e) {
        console.error('Failed to pause:', e);
      }
    } else if (status === 'paused' || status === 'error') {
      try {
        status = 'downloading';
        await LauncherService.StartInstall(installPath, createShortcut);
      } catch (e) {
        status = 'error';
        errorMessage = String(e);
        showToast(errorMessage);
      }
    } else if (status === 'update_available') {
      try {
        status = 'downloading';
        await LauncherService.StartUpdate(installPath);
      } catch (e) {
        showToast(String(e));
      }
    } else if (status === 'ready') {
      try {
        isPressed = true;
        setTimeout(() => {
          isPressed = false;
        }, 300);
        await LauncherService.LaunchGame(installPath, "");
      } catch (e) {
        isPressed = false;
        showToast(String(e));
      }
    }
  }

  async function handleModalConfirm(path: string, shortcut: boolean) {
    isModalOpen = false;
    installPath = path;
    createShortcut = shortcut;
    status = 'downloading';
    downloadPercent = 0;
    try {
      await LauncherService.StartInstall(path, shortcut);
    } catch (e) {
      status = 'error';
      errorMessage = String(e);
      showToast(errorMessage);
      refreshGameState();
    }
  }

  function toggleMenu(e: MouseEvent) {
    e.stopPropagation();
    isMenuOpen = !isMenuOpen;
  }

  function handleMenuSettings() {
    isMenuOpen = false;
    isSettingsOpen = true;
  }

  async function handleMenuShortcut() {
    isMenuOpen = false;
    try {
      await LauncherService.CreateDesktopShortcut(installPath);
      showToast('Desktop shortcut created');
    } catch (e) {
      showToast(String(e));
    }
  }

  async function handleMenuVerify() {
    isMenuOpen = false;
    if (status !== 'ready') return;
    status = 'verifying';
    verifyPercent = 0;
    try {
      await LauncherService.StartVerification(installPath);
    } catch (e) {
      showToast(String(e));
    }
  }

  async function handleMenuClearCache() {
    isMenuOpen = false;
    try {
      status = 'not_installed';
      downloadPercent = 0;
      downloadSpeed = 0;
      downloadedBytes = 0;
      errorMessage = '';
      verifyPercent = 0;

      await LauncherService.ClearDownloadCache(installPath);
      showToast('Download cache cleared');
      await refreshGameState();
    } catch (e) {
      showToast(String(e));
    }
  }

  const buttonLabel = $derived.by(() => {
    if (status === 'not_installed') {
      return 'Download';
    }
    if (status === 'downloading') {
      return isButtonHovered ? 'Pause' : 'Downloading';
    }
    if (status === 'paused') {
      return 'Resume';
    }
    if (status === 'error') {
      return isButtonHovered ? 'Retry' : 'Download Error';
    }
    if (status === 'verifying') {
      return verifyPercent > 0 ? `Verifying ${verifyPercent.toFixed(0)}%` : 'Verifying';
    }
    if (status === 'update_available') {
      return 'Update';
    }
    if (status === 'running') {
      return 'Running';
    }
    if (status === 'booting') {
      return 'Starting';
    }
    return 'Play';
  });

  const showFloatingCard = $derived.by(() => {
    if (!isButtonHovered && !isCardHovered) return false;
    return status === 'downloading' || status === 'paused' || status === 'error';
  });

  const cardTitle = $derived.by(() => {
    if (status === 'downloading') return 'Downloading';
    if (status === 'paused') return 'Paused';
    if (status === 'error') return 'Download Error';
    return '';
  });

  const cardSubtitle = $derived.by(() => {
    if (status === 'error') {
      return errorMessage || 'Unstable network connection. Please check your connection and try again.';
    }
    return '';
  });

  const displayPercent = $derived.by(() => {
    return Math.max(0, Math.min(100, Math.floor(downloadPercent)));
  });

  const displaySpeed = $derived.by(() => {
    if (status === 'paused' || status === 'error') return '0 MB/s';
    return formatSpeed(downloadSpeed);
  });

  const displayEta = $derived.by(() => {
    if (status === 'paused' || status === 'error') return '--:--:--';
    const total = totalDownloadBytes > 0 ? totalDownloadBytes : (diskSpace?.required_bytes ?? 62410385966);
    const remaining = Math.max(0, total - downloadedBytes);
    if (downloadSpeed <= 0) return '--:--:--';
    return formatEta(remaining / downloadSpeed);
  });

  const totalFormatted = $derived.by(() => {
    const total = totalDownloadBytes > 0 ? totalDownloadBytes : (diskSpace?.required_bytes ?? 62410385966);
    return formatBytes(total);
  });

  const downloadedFormatted = $derived.by(() => {
    return formatBytes(downloadedBytes);
  });
</script>

<div class="action-button-container" bind:this={menuContainer} data-no-drag>
  {#if toastMessage}
    <div class="toast-popup" role="status" aria-live="polite">
      {toastMessage}
    </div>
  {/if}

  {#if showFloatingCard}
    <div
      class="floating-card"
      role="tooltip"
      onmouseenter={() => (isCardHovered = true)}
      onmouseleave={() => (isCardHovered = false)}
    >
      <div class="card-header">
        <p class="card-title">{cardTitle}</p>
        {#if cardSubtitle}
          <p class="card-subtitle">{cardSubtitle}</p>
        {/if}
      </div>
      <div class="card-divider"></div>
      <div class="card-body">
        <div class="progress-stats-row">
          <span class="percent-val">{displayPercent}%</span>
          <span class="size-val"> ({downloadedFormatted} / {totalFormatted})</span>
        </div>
        <div class="progress-track-wrapper">
          <div class="slashes-container">
            {#each Array(45) as _}
              <div class="slash-item"></div>
            {/each}
          </div>
          <div
            class="progress-fill"
            style="transform: translateX({-100 + displayPercent}%);"
          ></div>
        </div>
        <div class="card-footer-row">
          <div class="footer-stat-item">
            <svg class="stat-icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M6.5916 0.588867C6.10649 0.588929 5.67239 0.881059 5.48613 1.32227L5.45195 1.41309L4.59746 4H6.70391L7.16973 2.58984L9.41192 2.58887L7.46563 7H10.3006L7.99981 11.1406L7.99981 8H5.38359L6.04375 6L3.93731 6L3.1375 8.42383C2.88142 9.20004 3.45879 9.9998 4.27617 10H5.99981V14.2285C6.00024 15.4662 7.64739 15.8933 8.24883 14.8115L12.7088 6.78223L12.7801 6.63086C13.0793 5.86391 12.5176 4.99922 11.66 4.99902L10.535 5L11.7381 2.27344L11.7928 2.12402C12.014 1.37273 11.453 0.589132 10.6404 0.588867L6.5916 0.588867Z" fill="white" fill-opacity="0.55"/>
            </svg>
            <span>{displaySpeed}</span>
          </div>
          <div class="footer-stat-item">
            <svg class="stat-icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M8 1C4.13401 1 1 4.13401 1 8C1 11.5264 3.60787 14.4414 7 14.9268V12.8994C4.71781 12.4361 3 10.4189 3 8C3 5.23858 5.23858 3 8 3C10.7614 3 13 5.23858 13 8C13 10.4189 11.2822 12.4361 9 12.8994V14.9268C12.3921 14.4414 15 11.5264 15 8C15 4.13401 11.866 1 8 1Z" fill="white" fill-opacity="0.55"/>
              <path d="M7.5 5.5V8.5H10.5" stroke="white" stroke-opacity="0.55" stroke-width="2"/>
            </svg>
            <span>{displayEta}</span>
          </div>
        </div>
      </div>
    </div>
  {/if}

  {#if isMenuOpen}
    <div class="context-menu" role="menu">
      <button
        type="button"
        class="menu-item"
        role="menuitem"
        onclick={handleMenuSettings}
      >
        Game Settings
      </button>

      <button
        type="button"
        class="menu-item"
        role="menuitem"
        onclick={handleMenuShortcut}
      >
        Create Desktop Shortcut
      </button>

      <button
        type="button"
        class="menu-item"
        role="menuitem"
        class:disabled={status !== 'ready'}
        disabled={status !== 'ready'}
        onclick={handleMenuVerify}
      >
        Check Game Integrity
      </button>

      <button
        type="button"
        class="menu-item"
        role="menuitem"
        class:disabled={status === 'verifying' || status === 'running'}
        disabled={status === 'verifying' || status === 'running' || status === 'booting'}
        onclick={handleMenuClearCache}
      >
        Clear Download Cache
      </button>
    </div>
  {/if}

  <div
    class="action-pill"
    class:is-running={status === 'running'}
    class:is-pressed={isPressed && status !== 'running'}
  >
    <button
      type="button"
      class="main-trigger"
      disabled={status === 'running'}
      onclick={handleMainAction}
      onmouseenter={() => (isButtonHovered = true)}
      onmouseleave={() => {
        isButtonHovered = false;
        isPressed = false;
      }}
      onpointerdown={() => {
        if (status !== 'running') isPressed = true;
      }}
      onpointerup={() => (isPressed = false)}
      onpointercancel={() => (isPressed = false)}
    >
      <div class="icon-slot">
        {#if status === 'not_installed'}
          <div class="icon-asset-wrap">
            <svg class="icon-frame" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect x="1" y="1" width="24" height="24" rx="12" stroke="#18171A" stroke-width="2"/>
            </svg>
            <div
              class="arrow-box"
              class:arrowAnimating={isArrowAnimating}
              onanimationend={handleArrowAnimationEnd}
            >
              <svg width="26" height="26" viewBox="0 0 27 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <path fill-rule="evenodd" clip-rule="evenodd" d="M11.5 8.5H15.5V12.9286L17.5 12.3571V14.5L13.5 18.5L9.5 14.5V12.3571L11.5 12.9286V8.5Z" fill="#18171A"/>
              </svg>
            </div>
          </div>

        {:else if status === 'downloading'}
          <div class="icon-asset-wrap">
            <svg class="progress-ring-svg" width="26" height="26" viewBox="0 0 26 26">
              <circle cx="13" cy="13" r="12" fill="none" stroke="#18171A" stroke-opacity="0.2" stroke-width="2"/>
              <circle
                cx="13"
                cy="13"
                r="12"
                fill="none"
                stroke="#18171A"
                stroke-width="2"
                stroke-linecap="round"
                stroke-dasharray="75.398"
                stroke-dashoffset={75.398 * (1 - Math.max(0, Math.min(100, downloadPercent)) / 100)}
              />
            </svg>
            {#if isButtonHovered}
              <svg class="inner-icon" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <path fill-rule="evenodd" clip-rule="evenodd" d="M12 8.5H8V17.5H12V8.5ZM18 8.5H14V17.5H18V8.5Z" fill="#18171A"/>
              </svg>
            {:else}
              <div class="arrowLoopAnimating inner-icon-wrap">
                <svg width="26" height="26" viewBox="0 0 27 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path fill-rule="evenodd" clip-rule="evenodd" d="M11.5 8.5H15.5V12.9286L17.5 12.3571V14.5L13.5 18.5L9.5 14.5V12.3571L11.5 12.9286V8.5Z" fill="#18171A"/>
                </svg>
              </div>
            {/if}
          </div>

        {:else if status === 'paused'}
          <div class="icon-asset-wrap">
            <svg class="progress-ring-svg" width="26" height="26" viewBox="0 0 26 26">
              <circle cx="13" cy="13" r="12" fill="none" stroke="#18171A" stroke-opacity="0.2" stroke-width="2"/>
              <circle
                cx="13"
                cy="13"
                r="12"
                fill="none"
                stroke="#18171A"
                stroke-width="2"
                stroke-linecap="round"
                stroke-dasharray="75.398"
                stroke-dashoffset={75.398 * (1 - Math.max(0, Math.min(100, downloadPercent)) / 100)}
              />
            </svg>
            {#if isButtonHovered}
              <div class="arrow-box" class:arrowAnimating={isArrowAnimating} onanimationend={handleArrowAnimationEnd}>
                <svg width="26" height="26" viewBox="0 0 27 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path fill-rule="evenodd" clip-rule="evenodd" d="M11.5 8.5H15.5V12.9286L17.5 12.3571V14.5L13.5 18.5L9.5 14.5V12.3571L11.5 12.9286V8.5Z" fill="#18171A"/>
                </svg>
              </div>
            {:else}
              <svg class="inner-icon" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <path fill-rule="evenodd" clip-rule="evenodd" d="M12 8.5H8V17.5H12V8.5ZM18 8.5H14V17.5H18V8.5Z" fill="#18171A"/>
              </svg>
            {/if}
          </div>

        {:else if status === 'error'}
          <div class="icon-asset-wrap">
            <svg class="progress-ring-svg" width="26" height="26" viewBox="0 0 26 26">
              <circle cx="13" cy="13" r="12" fill="none" stroke="#18171A" stroke-opacity="0.2" stroke-width="2"/>
              <circle
                cx="13"
                cy="13"
                r="12"
                fill="none"
                stroke="#18171A"
                stroke-width="2"
                stroke-linecap="round"
                stroke-dasharray="75.398"
                stroke-dashoffset={75.398 * (1 - Math.max(0, Math.min(100, downloadPercent)) / 100)}
              />
            </svg>
            {#if isButtonHovered}
              <div class="arrow-box" class:arrowAnimating={isArrowAnimating} onanimationend={handleArrowAnimationEnd}>
                <svg width="26" height="26" viewBox="0 0 27 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path fill-rule="evenodd" clip-rule="evenodd" d="M11.5 8.5H15.5V12.9286L17.5 12.3571V14.5L13.5 18.5L9.5 14.5V12.3571L11.5 12.9286V8.5Z" fill="#18171A"/>
                </svg>
              </div>
            {:else}
              <svg class="inner-icon" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <path d="M15 18.5H11V15.5H15V18.5ZM15 13.5H11V7.5H15V13.5Z" fill="#18171A"/>
              </svg>
            {/if}
          </div>

        {:else if status === 'verifying'}
          <div class="icon-asset-wrap">
            <div class="loading absolute inset-0 flex items-center justify-center">
              <svg width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <circle cx="13" cy="13" r="12" stroke="#18171A" stroke-opacity="0.2" stroke-width="2"/>
                <path d="M25 13C25 19.6274 19.6274 25 13 25" stroke="#18171A" stroke-width="2" stroke-linecap="round"/>
              </svg>
            </div>
            <svg class="inner-icon" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M12.9419 17.5H10.5L10.5 8.5L12.9419 8.5L17.5 13L12.9419 17.5Z" fill="#18171A"/>
            </svg>
          </div>

        {:else if status === 'update_available'}
          <div class="icon-asset-wrap">
            <svg class="icon-frame" width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect x="1" y="1" width="24" height="24" rx="12" stroke="#18171A" stroke-width="2"/>
            </svg>
            <div class="arrow-box" class:arrowAnimating={isArrowAnimating} onanimationend={handleArrowAnimationEnd}>
              <svg width="26" height="26" viewBox="0 0 27 26" fill="none" xmlns="http://www.w3.org/2000/svg">
                <path fill-rule="evenodd" clip-rule="evenodd" d="M11.5 8.5H15.5V12.9286L17.5 12.3571V14.5L13.5 18.5L9.5 14.5V12.3571L11.5 12.9286V8.5Z" fill="#18171A"/>
              </svg>
            </div>
          </div>

        {:else}
          <div class="icon-asset-wrap">
            <svg width="26" height="26" viewBox="0 0 26 26" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect x="1" y="1" width="24" height="24" rx="12" stroke="#18171A" stroke-width="2"/>
              <path d="M12.9419 17.5H10.5L10.5 8.5L12.9419 8.5L17.5 13L12.9419 17.5Z" fill="#18171A"/>
            </svg>
          </div>
        {/if}
      </div>

      <div class="label-slot">
        <span class="action-label">{buttonLabel}</span>
      </div>
    </button>

    <button
      type="button"
      class="hamburger-btn"
      class:is-active={isMenuOpen}
      title="Options"
      aria-label="Options"
      aria-expanded={isMenuOpen}
      onclick={toggleMenu}
    >
      <div class="hamburger-box">
        <span class="line line-1"></span>
        <span class="line line-2"></span>
        <span class="line line-3"></span>
      </div>
    </button>
  </div>
</div>

<InstallModal
  open={isModalOpen}
  installPath={installPath}
  createShortcut={createShortcut}
  diskSpace={diskSpace}
  onConfirm={handleModalConfirm}
  onCancel={() => (isModalOpen = false)}
/>

<SettingsModal
  open={isSettingsOpen}
  gameState={gameState}
  onClose={() => (isSettingsOpen = false)}
  onPathChange={(newPath) => {
    installPath = newPath;
    refreshGameState();
  }}
/>
