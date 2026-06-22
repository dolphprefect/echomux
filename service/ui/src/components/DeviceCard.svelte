<script>
  import { createEventDispatcher } from 'svelte'
  import { api } from '../lib/api.js'

  export let device
  export let isConnecting = false
  export let connectError = null

  const dispatch = createEventDispatcher()

  // Local volume tracks the slider during drag; syncs from prop when idle.
  let localVol = device.volume >= 0 ? device.volume : null
  let dragging = false

  $: if (!dragging) {
    localVol = device.volume >= 0 ? device.volume : null
  }

  $: volPending = localVol === null
  $: volPct = volPending ? '0%' : localVol.toFixed(1) + '%'
  $: fillColor = device.Muted ? 'var(--sub)' : 'var(--gold)'
  $: dotClass = device.Connected
    ? (device.Playing ? 'dot on playing' : 'dot on')
    : 'dot'

  async function doMute() {
    const muted = !device.Muted
    dispatch('muteChange', { mac: device.MAC, muted })
    try { await api('PUT', `/devices/${device.MAC}/mute`, { muted }) } catch(e) {
      dispatch('muteChange', { mac: device.MAC, muted: !muted })
    }
  }

  function onVolInput(e) {
    dragging = true
    localVol = parseInt(e.target.value)
  }

  async function commitVol(e) {
    dragging = false
    const level = parseInt(e.target.value)
    localVol = level
    dispatch('volumeChange', { mac: device.MAC, level })
    try { await api('PUT', `/devices/${device.MAC}/volume`, { level }) } catch(e) {}
  }
</script>

<div class="card {device.Connected ? '' : isConnecting ? 'connecting' : 'offline'}">
  <div class="card-top">
    <div class="card-identity">
      <div class={dotClass}></div>
      <div class="speaker-name">{device.Name}</div>
    </div>

    {#if device.Connected}
      <button class="btn-power" on:click={() => dispatch('disconnect', device.MAC)} title="Disconnect">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 3v9"/><path d="M6.2 5.2A8 8 0 1 0 17.8 5.2"/>
        </svg>
      </button>
    {:else if isConnecting}
      <span class="connecting-label"><span class="spinner-sm"></span>Connecting…</span>
    {:else}
      <div style="display:flex;align-items:center;gap:4px">
        <button class="btn-forget" on:click={() => dispatch('forget', device.MAC)} title="Forget speaker">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/>
            <path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/>
          </svg>
        </button>
        <button class="btn-connect" on:click={() => dispatch('connect', device.MAC)}>Connect</button>
      </div>
    {/if}
  </div>

  {#if connectError}
    <div class="connect-error">{connectError}</div>
  {/if}

  {#if device.Connected}
    <div class="card-body">
      <div class="vol-row">
        <button class="btn-mute" class:muted={device.Muted} on:click={doMute}>
          {#if device.Muted}
            <svg viewBox="0 0 24 24" fill="currentColor">
              <path d="M11 5 6 9H2v6h4l5 4V5z"/>
              <path d="m17 9 4 4m0-4-4 4" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
            </svg>
          {:else}
            <svg viewBox="0 0 24 24" fill="currentColor">
              <path d="M11 5 6 9H2v6h4l5 4V5z"/>
              <path d="M15.5 8.5a5 5 0 0 1 0 7" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
              <path d="M19 5a10 10 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
            </svg>
          {/if}
        </button>

        <input
          type="range" min="0" max="100"
          value={volPending ? 0 : localVol}
          disabled={volPending}
          style="--pct: {volPct}; --fill: {volPending ? 'var(--sub)' : fillColor}; --thumb: {volPending ? 'var(--sub)' : fillColor}"
          on:input={onVolInput}
          on:change={commitVol}
        >

        <span class="vol-pct">{volPending ? '–' : localVol + '%'}</span>
      </div>

      <div class="delay-row">
        <div class="delay-chip" role="button" tabindex="0"
          on:click={() => dispatch('openDelay', device)}
          on:keydown={e => (e.key === 'Enter' || e.key === ' ') && dispatch('openDelay', device)}>
          <span class="delay-chip-label">delay</span>
          <span class="delay-chip-val" class:live={(device.delay_ms || 0) > 0}>
            {device.delay_ms || 0} ms
          </span>
          <svg class="delay-chip-chevron" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="3,2 7,5 3,8"/>
          </svg>
        </div>
      </div>
    </div>
  {/if}
</div>
