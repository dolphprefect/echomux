import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import DeviceCard from './DeviceCard.svelte'

const baseDevice = {
  MAC: 'AA:BB:CC:DD:EE:FF',
  Name: 'Shelf Speaker',
  Connected: false,
  Playing: false,
  volume: 50,
  Muted: false,
  delay_ms: 0,
  Paired: true,
}

afterEach(() => vi.restoreAllMocks())

describe('DeviceCard', () => {
  it('renders the speaker name', () => {
    const { getByText } = render(DeviceCard, { props: { device: baseDevice } })
    expect(getByText('Shelf Speaker')).toBeTruthy()
  })

  it('shows connect button and forget button when disconnected', () => {
    const { container } = render(DeviceCard, { props: { device: baseDevice } })
    expect(container.querySelector('.btn-connect')).not.toBeNull()
    expect(container.querySelector('.btn-forget')).not.toBeNull()
  })

  it('shows disconnect button when connected', () => {
    const dev = { ...baseDevice, Connected: true }
    const { container } = render(DeviceCard, { props: { device: dev } })
    expect(container.querySelector('.btn-power')).not.toBeNull()
  })

  it('shows connecting spinner when isConnecting is true', () => {
    const { container } = render(DeviceCard, {
      props: { device: baseDevice, isConnecting: true },
    })
    expect(container.querySelector('.spinner-sm')).not.toBeNull()
    expect(container.textContent).toContain('Connecting')
  })

  it('shows connect error message', () => {
    const { container } = render(DeviceCard, {
      props: { device: baseDevice, connectError: 'Connection failed' },
    })
    expect(container.querySelector('.connect-error')).not.toBeNull()
    expect(container.textContent).toContain('Connection failed')
  })

  it('dispatches connect event on button click', async () => {
    const { container, component } = render(DeviceCard, { props: { device: baseDevice } })
    const handler = vi.fn()
    component.$on('connect', handler)

    await fireEvent.click(container.querySelector('.btn-connect'))
    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toBe('AA:BB:CC:DD:EE:FF')
  })

  it('dispatches disconnect event on power button click', async () => {
    const dev = { ...baseDevice, Connected: true }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('disconnect', handler)

    await fireEvent.click(container.querySelector('.btn-power'))
    expect(handler).toHaveBeenCalled()
  })

  it('dispatches forget event on forget button click', async () => {
    const { container, component } = render(DeviceCard, { props: { device: baseDevice } })
    const handler = vi.fn()
    component.$on('forget', handler)

    await fireEvent.click(container.querySelector('.btn-forget'))
    expect(handler).toHaveBeenCalled()
  })

  it('dispatches openDelay event on delay chip click', async () => {
    const dev = { ...baseDevice, Connected: true }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('openDelay', handler)

    const chip = container.querySelector('.delay-chip')
    await fireEvent.click(chip)
    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toEqual(dev)
  })

  it('shows delay chip with current ms when connected', () => {
    const dev = { ...baseDevice, Connected: true, delay_ms: 250 }
    const { container } = render(DeviceCard, { props: { device: dev } })
    expect(container.textContent).toContain('250 ms')
  })

  it('does not show card-body (volume/delay controls) when disconnected', () => {
    const { container } = render(DeviceCard, { props: { device: baseDevice } })
    expect(container.querySelector('.card-body')).toBeNull()
  })

  it('shows volume controls when connected', () => {
    const dev = { ...baseDevice, Connected: true }
    const { container } = render(DeviceCard, { props: { device: dev } })
    expect(container.querySelector('.vol-row')).not.toBeNull()
    expect(container.querySelector('.delay-row')).not.toBeNull()
  })

  it('dispatches volumeChange on slider commit', async () => {
    global.fetch = vi.fn().mockResolvedValue({ ok: true, status: 204, headers: { get: () => '' } })
    const dev = { ...baseDevice, Connected: true }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('volumeChange', handler)

    const slider = container.querySelector('input[type="range"]')
    await fireEvent.input(slider, { target: { value: '80' } })
    await fireEvent.change(slider, { target: { value: '80' } })

    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', level: 80 })
  })

  it('dispatches muteChange on mute button click', async () => {
    global.fetch = vi.fn().mockResolvedValue({ ok: true, status: 204, headers: { get: () => '' } })
    const dev = { ...baseDevice, Connected: true, Muted: false }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('muteChange', handler)

    const muteBtn = container.querySelector('.btn-mute')
    await fireEvent.click(muteBtn)

    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', muted: true })
  })

  it('shows offline card class when disconnected', () => {
    const { container } = render(DeviceCard, { props: { device: { ...baseDevice, Connected: false, Playing: false } } })
    expect(container.querySelector('.card.offline')).not.toBeNull()
  })

  it('shows dot on class when connected but not playing', () => {
    const { container } = render(DeviceCard, { props: { device: { ...baseDevice, Connected: true, Playing: false } } })
    expect(container.querySelector('.dot.on')).not.toBeNull()
  })

  it('shows dot on playing class when connected and playing', () => {
    const { container } = render(DeviceCard, { props: { device: { ...baseDevice, Connected: true, Playing: true } } })
    expect(container.querySelector('.dot.on.playing')).not.toBeNull()
  })

  it('shows connecting card class when isConnecting is true', () => {
    const { container } = render(DeviceCard, {
      props: { device: { ...baseDevice, Connected: false }, isConnecting: true },
    })
    expect(container.querySelector('.card.connecting')).not.toBeNull()
  })

  it('shows dash symbol when volume is pending (negative)', () => {
    const dev = { ...baseDevice, Connected: true, volume: -1 }
    const { container } = render(DeviceCard, { props: { device: dev } })
    expect(container.querySelector('.vol-pct').textContent).toBe('–')
  })

  it('shows 0% for volume=0 (zero is not treated as pending)', () => {
    const dev = { ...baseDevice, Connected: true, volume: 0 }
    const { container } = render(DeviceCard, { props: { device: dev } })
    expect(container.querySelector('.vol-pct').textContent).toBe('0%')
  })

  it('dispatches volumeChange exactly once even when the API call fails (fire-and-forget)', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('network'))
    const dev = { ...baseDevice, Connected: true }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('volumeChange', handler)

    const slider = container.querySelector('input[type="range"]')
    await fireEvent.input(slider, { target: { value: '70' } })
    await fireEvent.change(slider, { target: { value: '70' } })

    // Give the rejected promise time to settle; no second dispatch should follow.
    await new Promise(r => setTimeout(r, 50))
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler.mock.calls[0][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', level: 70 })
  })

  it('reverts optimistic mute on API failure', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('network'))
    const dev = { ...baseDevice, Connected: true, Muted: false }
    const { container, component } = render(DeviceCard, { props: { device: dev } })
    const handler = vi.fn()
    component.$on('muteChange', handler)

    const muteBtn = container.querySelector('.btn-mute')
    await fireEvent.click(muteBtn)

    // First call: optimistic mute=true
    // After API failure: second call reverts to mute=false
    expect(handler).toHaveBeenCalledTimes(2)
    expect(handler.mock.calls[0][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', muted: true })
    expect(handler.mock.calls[1][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', muted: false })
  })
})
