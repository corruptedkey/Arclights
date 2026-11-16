<script lang="ts">
  import { onMount } from 'svelte';
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

  let posX = $state(0);
  let posY = $state(0);
  let isDragging = false;
  let startX = 0;
  let startY = 0;

  $effect(() => {
    if (open) {
      if (gameState?.install_path) {
        currentPath = gameState.install_path.replace(/\\/g, '/');
      }
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
    }
  });

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

  async function handleUninstall() {
    if (!gameState?.version) return;
    const confirmUninstall = window.confirm('Are you sure you want to uninstall Arknights: Endfield? All game files will be removed.');
    if (!confirmUninstall) return;
    try {
      await LauncherService.ClearDownloadCache(currentPath);
      onClose();
    } catch (e) {
      console.error('Failed to uninstall game:', e);
    }
  }

  function onMouseDown(e: MouseEvent) {
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
  <div class="settings-backdrop" role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="settings-card"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-heading"
      tabindex="-1"
      onmousedown={onMouseDown}
      style="--drag-x: {posX}px; --drag-y: {posY}px; transform: translate(var(--drag-x, 0px), var(--drag-y, 0px));"
    >
      <button
        type="button"
        class="close-btn"
        aria-label="Close"
        onclick={onClose}
      >
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M1.5 1.5L12.5 12.5M1.5 12.5L12.5 1.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
        </svg>
      </button>

      <aside class="sidebar-col">
        <div class="settings-header">
          <svg class="gear-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="3"></circle>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path>
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
            <div class="path-text">{currentPath}</div>
            <div class="box-hint">(Use an SSD for better gaming experiences.)</div>

            <div class="action-divider"></div>

            <button type="button" class="action-link" onclick={handleChangePath}>
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

          <div class="settings-box">
            <div class="box-title">Rendering Mode</div>
            <div class="box-hint-mb">Try switching to another option if you encounter issues during launching</div>

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
  </div>
{/if}
