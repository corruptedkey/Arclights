<script lang="ts">
  import { LauncherService } from '../../bindings/arclights';
  import type { DiskSpaceInfo } from '../../bindings/arclights/models';

  let {
    open = false,
    installPath = '',
    createShortcut = true,
    diskSpace = null,
    onConfirm,
    onCancel
  }: {
    open: boolean;
    installPath: string;
    createShortcut?: boolean;
    diskSpace?: DiskSpaceInfo | null;
    onConfirm: (path: string, shortcut: boolean) => void;
    onCancel: () => void;
  } = $props();

  let currentPath = $state('');
  let currentShortcut = $state(true);
  let spaceInfo = $state<DiskSpaceInfo | null>(null);
  let isLoadingSpace = $state(false);

  let closing = $state(false);

  let posX = $state(0);
  let posY = $state(0);
  let isDragging = false;
  let startX = 0;
  let startY = 0;

  const CLOSE_DURATION = 250;

  $effect(() => {
    if (open) {
      closing = false;
      currentPath = (installPath || 'E:/Arknights Endfield').replace(/\\/g, '/');
      currentShortcut = createShortcut ?? true;
      if (diskSpace) {
        spaceInfo = diskSpace;
      }
      refreshSpace(currentPath);
    } else {
      posX = 0;
      posY = 0;
    }
  });

  function handleCancel() {
    if (closing) return;
    closing = true;
    setTimeout(() => {
      closing = false;
      onCancel();
    }, CLOSE_DURATION);
  }

  async function refreshSpace(path: string) {
    if (!path) return;
    isLoadingSpace = true;
    try {
      const res = await LauncherService.GetDiskSpace(path);
      if (res) {
        spaceInfo = res;
      }
    } catch (e) {
      console.warn('Failed to get disk space:', e);
    } finally {
      isLoadingSpace = false;
    }
  }

  async function chooseDirectory() {
    try {
      const selected = await LauncherService.SelectInstallDirectory();
      if (selected) {
        const normalized = selected.replace(/\\/g, '/');
        currentPath = normalized.endsWith('/Arknights Endfield')
          ? normalized
          : `${normalized.replace(/\/$/, '')}/Arknights Endfield`;
        await refreshSpace(currentPath);
      }
    } catch (e) {
      console.error('Failed to select directory:', e);
    }
  }

  function handlePathBlur() {
    refreshSpace(currentPath);
  }

  function handleConfirm() {
    if (closing) return;
    closing = true;
    setTimeout(() => {
      closing = false;
      onConfirm(currentPath, currentShortcut);
    }, CLOSE_DURATION);
  }

  function onMouseDown(e: MouseEvent) {
    if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .browse-btn, .shortcut-row')) {
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
  <div class="modal-backdrop install-modal-backdrop" class:closing role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="modal-card install-modal-card"
      class:closing
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
      tabindex="-1"
      onmousedown={onMouseDown}
      style="--drag-x: {posX}px; --drag-y: {posY}px; transform: translate(var(--drag-x, 0px), var(--drag-y, 0px));"
    >
      <h2 id="modal-title" class="modal-title">SYSTEM NOTIFICATION</h2>

      <div class="field-label">Game Install Path</div>
      <div class="path-input-wrap">
        <input
          type="text"
          class="path-input"
          bind:value={currentPath}
          onblur={handlePathBlur}
          spellcheck="false"
        />
        <div class="divider"></div>
        <button
          type="button"
          class="browse-btn"
          title="Browse Directory"
          aria-label="Browse Directory"
          onclick={chooseDirectory}
        >
          <svg class="folder-svg" width="18" height="18" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M8 10H12" stroke="currentColor" stroke-width="2"/>
            <path fill-rule="evenodd" clip-rule="evenodd" d="M6 2L7 4L13 4H15V6V12V14H13H5V12H13V6H7H5.76393L5.21115 4.89443L4.76393 4H3V10H1V4V2L3 2L6 2ZM1 12H3V14H1V12Z" fill="currentColor"/>
          </svg>
        </button>
      </div>

      <div class="space-info">
        {#if spaceInfo}
          Required space: {spaceInfo.required_gb.toFixed(2)} GB | Free space: {spaceInfo.free_gb.toFixed(2)} GB (Use an SSD for better gaming experiences.)
        {:else if isLoadingSpace}
          Calculating drive space...
        {:else}
          Required space: 58.09 GB | Free space: 137.61 GB (Use an SSD for better gaming experiences.)
        {/if}
      </div>

      <label class="shortcut-row">
        <input
          type="checkbox"
          class="native-checkbox-sr"
          bind:checked={currentShortcut}
        />
        <span class="checkbox-asset" aria-hidden="true">
          {#if currentShortcut}
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect width="16" height="16" rx="4" fill="white"/>
              <path d="M6.26118 11.6228L12.884 5M3.14999 7.1919L5.50692 9.54883" stroke="black" stroke-width="1.81818"/>
            </svg>
          {:else}
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect width="16" height="16" rx="4" fill="white" fill-opacity="0.25"/>
            </svg>
          {/if}
        </span>
        <span class="shortcut-text">Create desktop shortcut</span>
      </label>

      <div class="modal-actions">
        <button type="button" class="btn-cancel" onclick={handleCancel}>
          Cancel
        </button>
        <button type="button" class="btn-confirm" onclick={handleConfirm}>
          Confirm
        </button>
      </div>
    </div>
  </div>
{/if}
