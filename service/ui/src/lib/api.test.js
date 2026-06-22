import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api } from './api.js'

describe('api()', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('returns parsed JSON on 200 with json content-type', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => 'application/json' },
      json: () => Promise.resolve([{ MAC: 'AA:BB' }]),
    })
    const result = await api('GET', '/devices')
    expect(result).toEqual([{ MAC: 'AA:BB' }])
  })

  it('returns null on 200 with non-json content-type', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 204,
      headers: { get: () => '' },
    })
    const result = await api('POST', '/devices/AA:BB/connect')
    expect(result).toBeNull()
  })

  it('throws with server error text when response body is non-empty', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      headers: { get: () => '' },
      text: () => Promise.resolve('org.bluez.Error.AlreadyConnected'),
    })
    await expect(api('POST', '/foo')).rejects.toThrow('org.bluez.Error.AlreadyConnected')
  })

  it('throws with status code string when response body is blank', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      headers: { get: () => '' },
      text: () => Promise.resolve('  '),
    })
    await expect(api('POST', '/foo')).rejects.toThrow('500')
  })

  it('propagates when fetch itself throws (network error)', async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error('Failed to fetch'))
    await expect(api('GET', '/devices')).rejects.toThrow('Failed to fetch')
  })

  it('omits body and Content-Type for requests without a body argument', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => '' },
    })
    global.fetch = mockFetch
    await api('GET', '/devices')
    expect(mockFetch).toHaveBeenCalledWith('/devices', { method: 'GET', headers: {} })
  })

  it('sends JSON body with correct Content-Type header', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 204,
      headers: { get: () => '' },
    })
    global.fetch = mockFetch
    await api('PUT', '/devices/AA:BB/volume', { level: 80 })
    expect(mockFetch).toHaveBeenCalledWith('/devices/AA:BB/volume', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: '{"level":80}',
    })
  })
})
