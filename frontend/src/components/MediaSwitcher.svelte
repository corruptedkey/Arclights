<script lang="ts">
  let {
    hasVideo = false,
    mode = $bindable<'dynamic' | 'static'>('dynamic')
  }: {
    hasVideo?: boolean;
    mode?: 'dynamic' | 'static';
  } = $props();

  let isHovered = $state(false);

  const setMode = (m: 'dynamic' | 'static') => {
    mode = m;
    try {
      localStorage.setItem('arclights_media_mode', m);
    } catch {}
  };
</script>

{#if hasVideo}
  <div class="switch-bar-container" class:visible={isHovered} aria-hidden="true">
    <div class="switch-bar"></div>
  </div>

  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="hover-area"
    onmouseenter={() => (isHovered = true)}
    onmouseleave={() => (isHovered = false)}
  >
    <div
      class="switch-tabs"
      class:visible={isHovered}
      data-no-drag
      role="radiogroup"
      aria-label="Background display mode"
    >
      <div
        class="switch-indicator"
        class:is-static={mode === 'static'}
        aria-hidden="true"
      ></div>

      <button
        class="switch-tab"
        class:active={mode === 'dynamic'}
        type="button"
        data-no-drag
        role="radio"
        aria-checked={mode === 'dynamic'}
        aria-label="Video background"
        onclick={() => setMode('dynamic')}
      >
        <span class="switch-tab-icon">
          <svg width="20" height="16" viewBox="0 0 20 16" fill="none" aria-hidden="true">
            <path stroke="#fff" stroke-width="2" d="M3.592 15H19V1H1v11.085M2.01 15H1v-1.042" />
            <path fill="#fff" d="M13 8 8 4v8l5-4Z" />
          </svg>
        </span>
      </button>

      <button
        class="switch-tab"
        class:active={mode === 'static'}
        type="button"
        data-no-drag
        role="radio"
        aria-checked={mode === 'static'}
        aria-label="Static background"
        onclick={() => setMode('static')}
      >
        <span class="switch-tab-icon">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path
              fill="#fff"
              d="M19.571 18.144H14v-2h2.806l-2.88-4.801-8.64 4.8H11v2H.429V1.858H19.57v16.287Zm-17.142-2.7 12.216-6.788 2.926 4.876V3.857H2.43v11.586ZM6.57 6a2 2 0 1 1 0 4 2 2 0 0 1 0-4Z"
            />
          </svg>
        </span>
      </button>
    </div>
  </div>
{/if}
