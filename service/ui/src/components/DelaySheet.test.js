import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import DelaySheet from './DelaySheet.svelte'

afterEach(() => vi.restoreAllMocks())

function okFetch() {
  return vi.fn().mockResolvedValue({
    ok: true,
    status: 204,
    headers: { get: () => '' },
  })
}

function device(delay_ms = 100) {
  return { MAC: 'AA:BB:CC:DD:EE:FF', Name: 'Speaker', delay_ms }
}

describe('DelaySheet', () => {
  it('updates the displayed value after a successful API call', async () => {
    global.fetch = okFetch()
    const { getByText } = render(DelaySheet, { props: { device: device(100) } })

    expect(getByText('100')).toBeTruthy()
    await fireEvent.click(getByText('+50'))
    await waitFor(() => expect(getByText('150')).toBeTruthy())
  })

  it('rolls back to the previous value when the API call fails', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('network error'))
    const { getByText } = render(DelaySheet, { props: { device: device(100) } })

    expect(getByText('100')).toBeTruthy()
    await fireEvent.click(getByText('+50'))
    // Optimistic update shows 150 briefly, then reverts to 100 on API failure
    await waitFor(() => expect(getByText('100')).toBeTruthy())
  })

  it('clamps value to 0 when result would go below zero', async () => {
    global.fetch = okFetch()
    // Start at 30ms; clicking −50 would give −20 → clamped to 0
    const { getByText, container } = render(DelaySheet, { props: { device: device(30) } })

    const decBtn = container.querySelector('.btn-dec') // first .btn-dec is −50
    await fireEvent.click(decBtn)
    await waitFor(() => expect(getByText('0')).toBeTruthy())
  })

  it('dispatches updated event with the committed ms value on success', async () => {
    global.fetch = okFetch()
    const { getByText, component } = render(DelaySheet, { props: { device: device(100) } })
    const handler = vi.fn()
    component.$on('updated', handler)

    await fireEvent.click(getByText('+50'))
    await waitFor(() => expect(handler).toHaveBeenCalled())
    expect(handler.mock.calls[0][0].detail).toEqual({ mac: 'AA:BB:CC:DD:EE:FF', ms: 150 })
  })

  it('does not dispatch updated event when the API call fails', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('network error'))
    const { getByText, component } = render(DelaySheet, { props: { device: device(100) } })
    const handler = vi.fn()
    component.$on('updated', handler)

    await fireEvent.click(getByText('+50'))
    await waitFor(() => expect(getByText('100')).toBeTruthy())
    expect(handler).not.toHaveBeenCalled()
  })

  it('dispatches close event when the close button is clicked', async () => {
    global.fetch = okFetch()
    const { container, component } = render(DelaySheet, { props: { device: device(100) } })
    const handler = vi.fn()
    component.$on('close', handler)

    await fireEvent.click(container.querySelector('.btn-close'))
    expect(handler).toHaveBeenCalled()
  })

  it('dispatches close event when the backdrop is clicked', async () => {
    global.fetch = okFetch()
    const { container, component } = render(DelaySheet, { props: { device: device(100) } })
    const handler = vi.fn()
    component.$on('close', handler)

    await fireEvent.click(container.querySelector('.sheet-backdrop'))
    expect(handler).toHaveBeenCalled()
  })

  it('updates the displayed ms value while dragging the slider without calling the API', async () => {
    global.fetch = okFetch()
    const { getByText, container } = render(DelaySheet, { props: { device: device(0) } })
    expect(getByText('0')).toBeTruthy()

    const slider = container.querySelector('input[type="range"]')
    await fireEvent.input(slider, { target: { value: '500' } })
    await waitFor(() => expect(getByText('500')).toBeTruthy())
    expect(global.fetch).not.toHaveBeenCalled()
  })

  it('±10 buttons adjust the delay correctly', async () => {
    global.fetch = okFetch()
    const { container, getByText } = render(DelaySheet, { props: { device: device(100) } })

    const decBtns = container.querySelectorAll('.btn-dec')
    await fireEvent.click(decBtns[1]) // second .btn-dec is −10
    await waitFor(() => expect(getByText('90')).toBeTruthy())

    const incBtns = container.querySelectorAll('.btn-inc')
    await fireEvent.click(incBtns[0]) // first .btn-inc is +10
    await waitFor(() => expect(getByText('100')).toBeTruthy())
  })

  it('clamps value to 2000 when result would exceed maximum', async () => {
    global.fetch = okFetch()
    // Start at 1980ms; clicking +50 would give 2030 → clamped to 2000
    const { getByText } = render(DelaySheet, { props: { device: device(1980) } })

    await fireEvent.click(getByText('+50'))
    await waitFor(() => expect(getByText('2000')).toBeTruthy())
  })
})
