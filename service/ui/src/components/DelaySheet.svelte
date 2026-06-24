<script>
  import { createEventDispatcher } from 'svelte'
  import { api } from '../lib/api.js'

  export let device

  const dispatch = createEventDispatcher()

  let ms = device.delay_ms || 0

  $: pct = (ms / 2000 * 100).toFixed(1) + '%'
  $: live = ms > 0

  async function commit(val) {
    const clamped = Math.max(0, Math.min(2000, parseInt(val)))
    const prev = ms
    ms = clamped
    try {
      await api('PUT', `/devices/${device.MAC}/delay`, { ms: clamped }, device.node_id)
      dispatch('updated', { mac: device.MAC, ms: clamped })
    } catch(e) {
      ms = prev
    }
  }

  function adj(delta) {
    commit(ms + delta)
  }
</script>

<div class="sheet-overlay open">
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="sheet-backdrop" on:click={() => dispatch('close')}></div>
  <div class="sheet">
    <div class="sheet-header">
      <span class="sheet-title">{device.Name}</span>
      <button class="btn-close" on:click={() => dispatch('close')}>
        <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="2" y1="2" x2="12" y2="12"/><line x1="12" y1="2" x2="2" y2="12"/>
        </svg>
      </button>
    </div>

    <div class="delay-readout">
      <span class="delay-big" class:live>{ms}</span>
      <span class="delay-unit-big">ms</span>
    </div>

    <div class="delay-slider-wrap">
      <input
        type="range" min="0" max="2000"
        value={ms}
        style="--pct: {pct}; --fill: {live ? 'var(--gold)' : 'var(--sub)'}; --thumb: {live ? 'var(--gold)' : 'var(--sub)'}"
        on:input={e => ms = parseInt(e.target.value)}
        on:change={e => commit(e.target.value)}
      >
    </div>

    <div class="delay-btns">
      <button class="btn-adj btn-dec" on:click={() => adj(-50)}>&minus;50</button>
      <button class="btn-adj btn-dec" on:click={() => adj(-10)}>&minus;10</button>
      <button class="btn-adj btn-inc" on:click={() => adj(10)}>+10</button>
      <button class="btn-adj btn-inc" on:click={() => adj(50)}>+50</button>
    </div>
  </div>
</div>
