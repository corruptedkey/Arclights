<script lang="ts">
  import { onMount } from 'svelte';
  import { LauncherService } from '../bindings/arclights';
  import type { LauncherMediaBundle } from '../bindings/arclights/networks/index';
  import Socials from './components/Socials.svelte';
  import News from './components/News.svelte';
  import MediaSwitcher from './components/MediaSwitcher.svelte';
  import ActionButton from './components/ActionButton.svelte';
  import Header from './components/Header.svelte';
  import { modalState } from './modalState.svelte';

  let bundle = $state<LauncherMediaBundle | null>(null);
  let imageReady: boolean = $state(false);
  let videoReady: boolean = $state(false);

  const savedMode = (typeof localStorage !== 'undefined'
    ? localStorage.getItem('arclights_media_mode')
    : null) as 'dynamic' | 'static' | null;
  let bgMode = $state<'dynamic' | 'static'>(savedMode === 'static' ? 'static' : 'dynamic');

  let imgElement: HTMLImageElement | undefined = $state();
  let videoElement: HTMLVideoElement | undefined = $state();

  const bgImage = $derived(bundle?.main_bg_image?.url ?? '');
  const bgVideo = $derived(bundle?.main_bg_image?.video_url ?? '');

  $effect(() => {
    if (imgElement && imgElement.complete && imgElement.naturalWidth > 0) {
      imageReady = true;
    }
    if (videoElement) {
      if (videoElement.readyState >= 2) {
        videoReady = true;
      }
      if (bgMode === 'dynamic') {
        videoElement.play().catch(() => {});
      } else {
        videoElement.pause();
      }
    }
  });

  $effect(() => {
    if (typeof document !== 'undefined') {
      document.body.classList.toggle('has-modal-open', modalState.isAnyOpen);
    }
  });

  onMount(async () => {
    try {
      bundle = await LauncherService.GetLauncherMedia();
    } catch (err) {
      console.error('GetLauncherMedia failed:', err);
    }
  });
</script>

<div class="bg" aria-hidden="true">
  {#if bgImage}
    <img
      bind:this={imgElement}
      src={bgImage}
      alt=""
      class:ready={imageReady}
      onload={() => (imageReady = true)}
    />
  {/if}
  {#if bgVideo}
    <video
      bind:this={videoElement}
      src={bgVideo}
      class:ready={videoReady && bgMode === 'dynamic'}
      autoplay
      muted
      loop
      playsinline
      preload="auto"
      oncanplay={() => (videoReady = true)}
      onloadeddata={() => (videoReady = true)}
      onplaying={() => (videoReady = true)}
    ></video>
  {/if}
</div>

<main class="content">
  <Header />
  <MediaSwitcher hasVideo={!!bgVideo} bind:mode={bgMode} />
  <Socials sidebar={bundle?.sidebar} />
  <News banners={bundle?.banners} announcements={bundle?.announcements} />
  <ActionButton />
</main>
