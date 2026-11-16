<script lang="ts">
  import { Browser } from '@wailsio/runtime';
  import type { BannerItem, AnnouncementTab, AnnouncementItem } from '../../bindings/arclights/networks/index';

  let {
    banners = [],
    announcements = []
  }: {
    banners?: BannerItem[] | null;
    announcements?: AnnouncementTab[] | null;
  } = $props();

  const tabs = $derived(
    (announcements ?? []).filter(
      (t): t is AnnouncementTab & { announcements: AnnouncementItem[] } =>
        !!t && Array.isArray(t.announcements) && t.announcements.length > 0
    )
  );

  const bannerList = $derived(banners ?? []);

  let activeTab = $state(0);
  let currentBanner = $state(0);
  let isHovered = $state(false);

  let tabButtons: (HTMLButtonElement | null)[] = $state([]);

  let indicatorStyle = $derived.by(() => {
    const el = tabButtons[activeTab];
    if (!el) return 'opacity: 0;';
    return `transform: translateX(${el.offsetLeft}px); width: ${el.offsetWidth-18}px; opacity: 1;`;
  });

  $effect(() => {
    if (bannerList.length <= 1 || isHovered) return;
    const timer = setInterval(() => {
      currentBanner = (currentBanner + 1) % bannerList.length;
    }, 5000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (currentBanner >= bannerList.length && bannerList.length > 0) {
      currentBanner = 0;
    }
  });

  const nextBanner = () => {
    if (bannerList.length > 1) {
      currentBanner = (currentBanner + 1) % bannerList.length;
    }
  };

  const prevBanner = () => {
    if (bannerList.length > 1) {
      currentBanner = (currentBanner - 1 + bannerList.length) % bannerList.length;
    }
  };

  const openUrl = (url?: string | null) => {
    if (url) Browser.OpenURL(url);
  };

  function formatDate(ts?: string | number | null): string {
    if (!ts) return '';
    const n = Number(ts);
    if (isNaN(n) || n <= 0) return '';
    const date = new Date(n);
    const now = new Date();
    const mm = String(date.getMonth() + 1).padStart(2, '0');
    const dd = String(date.getDate()).padStart(2, '0');
    if (date.getFullYear() === now.getFullYear()) {
      return `${mm}/${dd}`;
    }
    return `${date.getFullYear()}/${mm}/${dd}`;
  }
</script>

{#if bannerList.length > 0 || tabs.length > 0}
  <section class="news-container" data-no-drag aria-label="News and Banners">
    {#if bannerList.length > 0}
      <div
        class="banner-wrap"
        role="region"
        aria-label="Banner carousel"
        onmouseenter={() => (isHovered = true)}
        onmouseleave={() => (isHovered = false)}
      >
        <div class="banner-slider" style="transform: translateX(-{currentBanner * 100}%);">
          {#each bannerList as banner, i (banner.id || banner.url || i)}
            <button
              class="banner-slide"
              type="button"
              onclick={() => openUrl(banner.jump_url)}
              aria-label="Open banner"
            >
              <img src={banner.url} alt="" loading="lazy" />
            </button>
          {/each}
        </div>

        {#if bannerList.length > 1}
          <button
            class="nav-btn prev"
            type="button"
            onclick={prevBanner}
            aria-label="Previous banner"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M10 12L6 8L10 4"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
          <button
            class="nav-btn next"
            type="button"
            onclick={nextBanner}
            aria-label="Next banner"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M6 4L10 8L6 12"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>

          <div class="indicators" role="tablist" aria-label="Banner pagination">
            {#each bannerList as _, idx (idx)}
              <button
                class="indicator-dot"
                type="button"
                role="tab"
                aria-selected={idx === currentBanner}
                aria-label="Slide {idx + 1}"
                onclick={() => (currentBanner = idx)}
              >
                <span class="bar" class:active={idx === currentBanner}></span>
              </button>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    {#if tabs.length > 0}
      {@const activeTabItem = tabs[activeTab] || tabs[0]}
      <div class="announcements-wrap" class:full-radius={bannerList.length === 0}>
        <div class="tabs-header" role="tablist">
          <div class="tabs-list">
            {#each tabs as tab, i (tab.tab_id || tab.tabName || i)}
              <button
                bind:this={tabButtons[i]}
                class="tab-btn"
                class:active={i === activeTab}
                type="button"
                role="tab"
                aria-selected={i === activeTab}
                onclick={() => (activeTab = i)}
              >
                {tab.tabName}
              </button>
            {/each}
          </div>
          <div class="tab-indicator" style={indicatorStyle}></div>
        </div>

        <div class="news-list" role="tabpanel">
          {#each activeTabItem.announcements as item, idx (item.id || item.content + idx)}
            <button
              class="news-row"
              type="button"
              onclick={() => openUrl(item.jump_url)}
              title={item.content}
            >
              <div class="row-left">
                {#if item.pin === 1}
                  <svg class="pin" width="14" height="14" viewBox="0 0 14 14" fill="none" aria-label="Pinned">
                    <rect width="14" height="14" rx="2" fill="#fdfc00" />
                    <path
                      d="M6.984 5.769L10.02 8.805M6.984 5.769L3.948 8.805M6.984 5.769V11.323M3.486 3.971H10.428"
                      stroke="#000"
                      stroke-width="1.5"
                      stroke-linecap="round"
                    />
                  </svg>
                {/if}
                <span class="title">{item.content}</span>
              </div>
              <span class="date">{formatDate(item.start_ts)}</span>
            </button>
          {/each}
        </div>
      </div>
    {/if}
  </section>
{/if}
