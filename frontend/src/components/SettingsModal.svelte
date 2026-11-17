<script lang="ts">
  import { LauncherService } from '../../bindings/arclights';
  import type { GameStateInfo } from '../../bindings/arclights/models';

  let {
    open = false,
    gameState = null,
    onClose,
    onPathChange
  }: {
    open: boolean;
    gameState?: GameStateInfo | null;
    onClose: () => void;
    onPathChange?: (newPath: string) => void;
  } = $props();

  let activeTab = $state<'manage' | 'advanced'>('manage');
  let currentPath = $state('E:/Arknights Endfield');
  let renderingMode = $state<'vk' | 'dx'>('vk');
  let launchCommand = $state('');
  let troubleshootingMode = $state(false);

  let closing = $state(false);

  let posX = $state(0);
  let posY = $state(0);
  let isDragging = false;
  let startX = 0;
  let startY = 0;

  let showUninstallConfirm = $state(false);
  let isUninstalling = $state(false);
  let cancelBtnRef = $state<HTMLButtonElement | null>(null);
  let confirmBtnRef = $state<HTMLButtonElement | null>(null);
  let previouslyFocusedElement: HTMLElement | null = null;

  const CLOSE_DURATION = 250;

  $effect(() => {
    if (gameState?.install_path) {
      currentPath = gameState.install_path.replace(/\\/g, '/');
    }
  });

  $effect(() => {
    if (showUninstallConfirm) {
      previouslyFocusedElement = document.activeElement as HTMLElement;
      queueMicrotask(() => {
        cancelBtnRef?.focus();
      });
    } else if (previouslyFocusedElement) {
      previouslyFocusedElement.focus();
      previouslyFocusedElement = null;
    }
  });

  $effect(() => {
    if (open) {
      closing = false;
      if (typeof localStorage !== 'undefined') {
        const savedRender = localStorage.getItem('arclights_rendering_mode');
        if (savedRender === 'dx' || savedRender === 'vk') {
          renderingMode = savedRender;
        }
        const savedCmd = localStorage.getItem('arclights_launch_command');
        if (savedCmd !== null) {
          launchCommand = savedCmd;
        }
        const savedTrouble = localStorage.getItem('arclights_troubleshooting');
        if (savedTrouble !== null) {
          troubleshootingMode = savedTrouble === 'true';
        }
      }

      LauncherService.GetLauncherSettings().then((cfg) => {
        if (cfg) {
          if (cfg.rendering_mode === 'dx' || cfg.rendering_mode === 'vk') {
            renderingMode = cfg.rendering_mode;
            localStorage.setItem('arclights_rendering_mode', renderingMode);
          }
          if (typeof cfg.launch_command === 'string') {
            launchCommand = cfg.launch_command;
            localStorage.setItem('arclights_launch_command', launchCommand);
          }
          troubleshootingMode = Boolean(cfg.troubleshooting_mode);
          localStorage.setItem('arclights_troubleshooting', String(troubleshootingMode));
        }
      }).catch((err) => {
        console.warn('Failed to get launcher settings in SettingsModal:', err);
      });

      const handleKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          if (showUninstallConfirm) {
            e.stopPropagation();
            handleCancelUninstall();
          } else {
            handleClose();
          }
        }
      };

      window.addEventListener('keydown', handleKeyDown);
      return () => {
        window.removeEventListener('keydown', handleKeyDown);
      };
    } else {
      posX = 0;
      posY = 0;
      showUninstallConfirm = false;
      isUninstalling = false;
    }
  });

  function handleClose() {
    if (closing) return;
    closing = true;
    showUninstallConfirm = false;
    setTimeout(() => {
      closing = false;
      onClose();
    }, CLOSE_DURATION);
  }

  async function setRenderingMode(mode: 'vk' | 'dx') {
    renderingMode = mode;
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('arclights_rendering_mode', mode);
    }
    try {
      const current = await LauncherService.GetLauncherSettings();
      current.rendering_mode = mode;
      await LauncherService.SaveLauncherSettings(current);
    } catch (e) {
      console.error('Failed to save rendering mode:', e);
    }
  }

  async function handleCommandInput(e: Event) {
    const target = e.target as HTMLInputElement;
    launchCommand = target.value.slice(0, 500);
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('arclights_launch_command', launchCommand);
    }
    try {
      const current = await LauncherService.GetLauncherSettings();
      current.launch_command = launchCommand;
      await LauncherService.SaveLauncherSettings(current);
    } catch (err) {
      console.error('Failed to save launch command:', err);
    }
  }

  async function toggleTroubleshooting() {
    troubleshootingMode = !troubleshootingMode;
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('arclights_troubleshooting', String(troubleshootingMode));
    }
    try {
      const current = await LauncherService.GetLauncherSettings();
      current.troubleshooting_mode = troubleshootingMode;
      await LauncherService.SaveLauncherSettings(current);
    } catch (err) {
      console.error('Failed to save troubleshooting mode:', err);
    }
  }

  async function handleChangePath() {
    try {
      const selected = await LauncherService.SelectInstallDirectory();
      if (selected) {
        const normalized = selected.replace(/\\/g, '/');
        currentPath = normalized.endsWith('/Arknights Endfield')
          ? normalized
          : `${normalized.replace(/\/$/, '')}/Arknights Endfield`;
        if (onPathChange) {
          onPathChange(currentPath);
        }
      }
    } catch (e) {
      console.error('Failed to select directory:', e);
    }
  }

  async function handleOpenDirectory() {
    try {
      await LauncherService.OpenGameDirectory(currentPath);
    } catch (e) {
      console.error('Failed to open directory:', e);
    }
  }

  function handleUninstall() {
    if (!gameState?.version) return;
    showUninstallConfirm = true;
  }

  function handleCancelUninstall() {
    if (isUninstalling) return;
    showUninstallConfirm = false;
  }

  function handleUninstallDialogKeydown(e: KeyboardEvent) {
    if (e.key !== 'Tab' || isUninstalling || !cancelBtnRef || !confirmBtnRef) return;

    if (e.shiftKey && document.activeElement === cancelBtnRef) {
      e.preventDefault();
      confirmBtnRef.focus();
    } else if (!e.shiftKey && document.activeElement === confirmBtnRef) {
      e.preventDefault();
      cancelBtnRef.focus();
    }
  }

  async function handleConfirmUninstall() {
    if (isUninstalling) return;
    isUninstalling = true;
    try {
      await LauncherService.UninstallGame(currentPath);
      showUninstallConfirm = false;
      handleClose();
    } catch (e) {
      console.error('Failed to uninstall game:', e);
    } finally {
      isUninstalling = false;
    }
  }

  function onMouseDown(e: MouseEvent) {
    if (showUninstallConfirm) return;
    if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .action-link, .close-btn, .tab-btn')) {
      return;
    }
    isDragging = true;
    startX = e.clientX - posX;
    startY = e.clientY - posY;
    window.addEventListener('mousemove', onMouseMove);
    window.addEventListener('mouseup', onMouseUp);
  }

  function onMouseMove(e: MouseEvent) {
    if (!isDragging) return;
    posX = e.clientX - startX;
    posY = e.clientY - startY;
  }

  function onMouseUp() {
    isDragging = false;
    window.removeEventListener('mousemove', onMouseMove);
    window.removeEventListener('mouseup', onMouseUp);
  }
</script>

{#if open}
  <div class="settings-backdrop" class:closing role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="settings-card"
      class:closing
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-heading"
      tabindex="-1"
      onmousedown={onMouseDown}
      style="--drag-x: {posX}px; --drag-y: {posY}px; transform: translate(var(--drag-x, 0px), var(--drag-y, 0px));"
    >


      <aside class="sidebar-col" inert={showUninstallConfirm}>
        <div class="settings-header">
          <svg class="gear-icon" width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path fill-rule="evenodd" clip-rule="evenodd" d="M4.36128 3.22889L3.61641 2.48401L2.2022 3.89823L2.99448 4.69051C2.50552 5.42858 2.17543 6.28107 2.05297 7.19922L0.5 7.19922V9.19922H2.11986C2.30316 10.1028 2.68953 10.9327 3.22926 11.6392L2.48445 12.384L3.89866 13.7982L4.69104 13.0059C5.42898 13.4946 6.28128 13.8246 7.19922 13.947V15.5H9.19921L9.19922 13.8801C9.96581 13.7246 10.6794 13.423 11.3095 13.0055L12.1017 13.7977L13.5159 12.3835L12.7711 11.6387C13.3106 10.9323 13.6969 10.1026 13.8801 9.19922H15.5V7.19922H13.947C13.8246 6.28129 13.4946 5.42899 13.0059 4.69104L13.7982 3.89875L12.3839 2.48453L11.6392 3.22927C10.9327 2.68953 10.1028 2.30316 9.19922 2.11986L9.19922 0.5H7.19922V2.05297C6.14117 2.19408 5.17032 2.61093 4.36128 3.22889ZM8 12.4C10.4301 12.4 12.4 10.4301 12.4 8C12.4 5.56995 10.4301 3.6 8 3.6C5.56995 3.6 3.6 5.56995 3.6 8C3.6 10.4301 5.56995 12.4 8 12.4ZM8 10C9.10457 10 10 9.10457 10 8C10 6.89543 9.10457 6 8 6C6.89543 6 6 6.89543 6 8C6 9.10457 6.89543 10 8 10Z" fill="white"/>
          </svg>
          <span id="settings-heading" class="settings-title">Settings</span>
        </div>

        <nav class="tab-list">
          <button
            type="button"
            class="tab-btn"
            class:active={activeTab === 'manage'}
            onclick={() => (activeTab = 'manage')}
          >
            Manage Games
          </button>
          <button
            type="button"
            class="tab-btn"
            class:active={activeTab === 'advanced'}
            onclick={() => (activeTab = 'advanced')}
          >
            Advanced Settings
          </button>
        </nav>
      </aside>

      <div class="right-col" inert={showUninstallConfirm}>
          <div class="top-bar">
        <button
            type="button"
            class="close-btn"
            aria-label="Close"
            onclick={handleClose}
        >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
            <rect x="12" y="11.2928" width="1" height="1" transform="rotate(45 12 11.2928)" stroke="currentColor"/>
            <path fill-rule="evenodd" clip-rule="evenodd" d="M13.5562 9.31353L17.0917 5.778L18.5059 7.19221L14.9704 10.7277L13.5562 9.31353ZM5.77746 17.0912L9.31299 13.5556L10.7272 14.9698L7.19167 18.5054L5.77746 17.0912ZM14.687 13.5556L18.2225 17.0912L16.8083 18.5054L13.2728 14.9698L14.687 13.5556ZM6.90883 5.77745L10.4444 9.31298L9.03015 10.7272L5.49462 7.19166L6.90883 5.77745Z" fill="currentColor"/>
            </svg>
        </button>
    </div>

      <main class="content-panel">
        {#if activeTab === 'manage'}
          <div class="game-meta-row">
            <div class="game-avatar-wrap">
              <img src="/endfield_avatar.png" alt="Arknights: Endfield" class="game-avatar" />
              <div class="game-titles">
                <div class="game-name">Arknights: Endfield</div>
                <div class="game-ver">Game Version: {gameState?.version || '--'}</div>
              </div>
            </div>

            <button
              type="button"
              class="uninstall-btn"
              class:disabled={!gameState?.version}
              disabled={!gameState?.version}
              onclick={handleUninstall}
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="3 6 5 6 21 6"></polyline>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
              </svg>
              <span>Uninstall game</span>
            </button>
          </div>

          <div class="settings-box">
            <div class="box-title">Game Install Path</div>
            <div class="card-divider"></div>

            <div class="path-text">{currentPath}</div>
            <div class="box-hint">(Use an SSD for better gaming experiences.)</div>

            <div class="action-list">
                <button type="button" class="action-link muted" onclick={handleChangePath}>
                  <span>Change game install path</span>
                  <svg class="chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="9 18 15 12 9 6"></polyline>
                  </svg>
                </button>

                <button type="button" class="action-link" onclick={handleOpenDirectory}>
                  <span>Open file directory</span>
                  <svg class="chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="9 18 15 12 9 6"></polyline>
                  </svg>
                </button>
            </div>
          </div>

          <div class="settings-box">
            <div class="box-title">Rendering Mode</div>
            <div class="box-hint-sub">Try switching to another option if you encounter issues during launching</div>
            <div class="card-divider"></div>

            <div class="radio-group">
              <button
                type="button"
                class="radio-option"
                class:selected={renderingMode === 'vk'}
                onclick={() => setRenderingMode('vk')}
              >
                <span class="radio-indicator">
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.5"/>
                    {#if renderingMode === 'vk'}
                      <circle cx="8" cy="8" r="4" fill="currentColor"/>
                    {/if}
                  </svg>
                </span>
                <span class="radio-label">Vulkan (Recommended)</span>
              </button>

              <button
                type="button"
                class="radio-option"
                class:selected={renderingMode === 'dx'}
                onclick={() => setRenderingMode('dx')}
              >
                <span class="radio-indicator">
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.5"/>
                    {#if renderingMode === 'dx'}
                      <circle cx="8" cy="8" r="4" fill="currentColor"/>
                    {/if}
                  </svg>
                </span>
                <span class="radio-label">DirectX 11 (Best GPU compatibility)</span>
              </button>
            </div>
          </div>

        {:else}
          <div class="settings-box">
            <div class="box-title">Launch Command</div>
            <div class="card-divider"></div>
            <div class="cmd-input-wrap">
              <input
                type="text"
                class="cmd-input"
                placeholder="Enter"
                value={launchCommand}
                oninput={handleCommandInput}
                spellcheck="false"
                maxlength="500"
              />
              <span class="char-count">{launchCommand.length}/500</span>
            </div>
            <div class="box-hint-mt">Leave this blank unless necessary</div>
          </div>

          <div class="settings-box">
            <div class="box-title">Launch Options</div>
            <div class="card-divider"></div>
            <div class="option-row">
              <span class="option-label">Troubleshooting Mode</span>
              <button
                type="button"
                class="toggle-switch"
                class:active={troubleshootingMode}
                onclick={toggleTroubleshooting}
                aria-label="Troubleshooting Mode"
                aria-pressed={troubleshootingMode}
              >
                {#if troubleshootingMode}
                  <span class="toggle-text on">ON</span>
                  <span class="toggle-circle active"></span>
                {:else}
                  <span class="toggle-circle">
                    <span class="circle-dash"></span>
                  </span>
                  <span class="toggle-text off">OFF</span>
                {/if}
              </button>
            </div>
            <div class="box-hint-mt">Enable this to generate logs for customer support if you have trouble running the game.</div>
          </div>
        {/if}
      </main>
      </div>

      {#if showUninstallConfirm}
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div
          class="notification-overlay"
          role="presentation"
          onclick={handleCancelUninstall}
          onmousedown={(e) => e.stopPropagation()}
        >
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <div
            class="notification-card"
            role="dialog"
            aria-modal="true"
            aria-labelledby="notification-heading"
            tabindex="-1"
            onclick={(e) => e.stopPropagation()}
            onkeydown={handleUninstallDialogKeydown}
          >
            <div id="notification-heading" class="notification-title">SYSTEM NOTIFICATION</div>
            <div class="notification-body">
              Are you sure you want to uninstall Arknights: Endfield? All game files will be removed.
            </div>
            <div class="notification-actions">
              <button
                type="button"
                class="notification-btn-cancel"
                bind:this={cancelBtnRef}
                onclick={handleCancelUninstall}
                disabled={isUninstalling}
              >
                Cancel
              </button>
              <button
                type="button"
                class="notification-btn-confirm"
                bind:this={confirmBtnRef}
                onclick={handleConfirmUninstall}
                disabled={isUninstalling}
              >
                {isUninstalling ? 'Uninstalling...' : 'Confirm'}
              </button>
            </div>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
