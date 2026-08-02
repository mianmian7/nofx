import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LanguageProvider } from '../../contexts/LanguageContext'
import { OrderBook } from './OrderBook'

const getDepthMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/api', () => ({ api: { getDepth: getDepthMock } }))

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  readonly url: string
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {}

  emitClose() {
    this.onclose?.()
  }

  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
  }
}

describe('OrderBook', () => {
  let flushFrame: FrameRequestCallback | undefined

  beforeEach(() => {
    FakeWebSocket.instances = []
    localStorage.setItem('language', 'zh')
    localStorage.removeItem('orderBookDisplayMode')
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.stubGlobal(
      'requestAnimationFrame',
      vi.fn((callback: FrameRequestCallback) => {
        flushFrame = callback
        return 1
      })
    )
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
    getDepthMock.mockReset()
    getDepthMock.mockResolvedValue({
      lastUpdateId: 1,
      bids: [['4119.06', '15.874']],
      asks: [['4119.07', '1.590']],
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('renders Binance Futures b/a partial-depth frames as a live book', () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )

    expect(FakeWebSocket.instances[0]?.url).toContain('xauusdt@depth20@100ms')
    act(() => {
      FakeWebSocket.instances[0].emit({
        e: 'depthUpdate',
        s: 'XAUUSDT',
        b: [['4119.06', '15.874']],
        a: [['4119.07', '1.590']],
      })
      flushFrame?.(250)
    })

    expect(screen.getByText('4,119.06')).toBeInTheDocument()
    expect(screen.getByText('4,119.07')).toBeInTheDocument()
    expect(screen.getByText('4,119.065')).toBeInTheDocument()
    expect(screen.getByText(/价差 0\.01/)).toBeInTheDocument()
    expect(screen.getByText('15.87')).toBeInTheDocument()
    expect(screen.getByText('1.59')).toBeInTheDocument()
    expect(screen.getByText(/● 实时/)).toBeInTheDocument()
  })

  it('defaults to smooth mode and commits only the latest frame every 250ms', async () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )
    await waitFor(() =>
      expect(screen.getByText('4,119.06')).toBeInTheDocument()
    )

    act(() => {
      FakeWebSocket.instances[0].emit({
        b: [['4120.00', '2']],
        a: [['4120.01', '3']],
      })
      flushFrame?.(100)
    })
    expect(screen.queryByText('4,120.00')).not.toBeInTheDocument()

    act(() => {
      FakeWebSocket.instances[0].emit({
        b: [['4121.00', '4']],
        a: [['4121.01', '5']],
      })
      flushFrame?.(249)
    })
    expect(screen.queryByText('4,121.00')).not.toBeInTheDocument()

    act(() => flushFrame?.(250))
    expect(screen.getByText('4,121.00')).toBeInTheDocument()
    expect(screen.queryByText('4,120.00')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '平滑' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
  })

  it('allows realtime mode to commit the next venue frame immediately', async () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )
    await waitFor(() =>
      expect(screen.getByText('4,119.06')).toBeInTheDocument()
    )

    fireEvent.click(screen.getByRole('button', { name: '实时更新' }))
    act(() => {
      FakeWebSocket.instances[0].emit({
        b: [['4122.00', '2']],
        a: [['4122.01', '3']],
      })
      flushFrame?.(1)
    })

    expect(screen.getByText('4,122.00')).toBeInTheDocument()
    expect(localStorage.getItem('orderBookDisplayMode')).toBe('realtime')
  })

  it('drops non-finite price and size levels', () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )

    act(() => {
      FakeWebSocket.instances[0].emit({
        b: [
          ['NaN', '2'],
          ['4119.06', '15.874'],
        ],
        a: [
          ['4119.07', 'Infinity'],
          ['4119.08', '1.590'],
        ],
      })
      flushFrame?.(250)
    })

    expect(screen.queryByText('NaN')).not.toBeInTheDocument()
    expect(screen.queryByText('Infinity')).not.toBeInTheDocument()
    expect(screen.getByText('15.87')).toBeInTheDocument()
    expect(screen.getByText('1.59')).toBeInTheDocument()
  })

  it('seeds visible depth from REST while the WebSocket is connecting', async () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )

    await waitFor(() =>
      expect(screen.getByText('4,119.06')).toBeInTheDocument()
    )
    expect(screen.getByText('4,119.07')).toBeInTheDocument()
    expect(getDepthMock).toHaveBeenCalledWith('XAUUSDT', 20, true)
    expect(screen.getByText(/○ 同步中/)).toBeInTheDocument()
  })

  it('polls the selected native venue without opening a Binance socket', async () => {
    render(
      <LanguageProvider>
        <OrderBook symbol="BTC-USDT-SWAP" exchange="okx" />
      </LanguageProvider>
    )

    await waitFor(() =>
      expect(screen.getByText('4,119.06')).toBeInTheDocument()
    )

    expect(getDepthMock).toHaveBeenCalledWith('BTCUSDT', 20, 'okx', true)
    expect(FakeWebSocket.instances).toHaveLength(0)
    expect(screen.getByText(/OKX USDⓈ-M/)).toBeInTheDocument()
  })

  it('ignores a stale WebSocket frame after the symbol changes', () => {
    const { rerender } = render(
      <LanguageProvider>
        <OrderBook symbol="MUUSDT" />
      </LanguageProvider>
    )
    const staleSocket = FakeWebSocket.instances[0]

    act(() => {
      staleSocket.emit({
        b: [['100.00', '2']],
        a: [['100.01', '3']],
      })
      flushFrame?.(250)
    })
    expect(screen.getByText('100.00')).toBeInTheDocument()

    rerender(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )
    act(() => {
      staleSocket.emit({
        b: [['100.00', '2']],
        a: [['100.01', '3']],
      })
      flushFrame?.(500)
    })

    expect(screen.queryByText('100.00')).not.toBeInTheDocument()
    expect(screen.queryByText('100.01')).not.toBeInTheDocument()
  })

  it('keeps REST depth visible during a WebSocket reconnect', async () => {
    vi.useFakeTimers()
    render(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )
    await act(async () => Promise.resolve())
    expect(screen.getByText('4,119.06')).toBeInTheDocument()

    act(() => FakeWebSocket.instances[0].emitClose())
    expect(screen.getByText(/○ 离线/)).toBeInTheDocument()
    expect(screen.getByText('4,119.06')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1500))
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(FakeWebSocket.instances[1].url).toContain('xauusdt@depth20@100ms')
    vi.useRealTimers()
  })

  it('ignores a stale REST response after the symbol changes', async () => {
    let resolveMU!: (value: unknown) => void
    getDepthMock.mockImplementation((symbol: string) => {
      if (symbol === 'MUUSDT') {
        return new Promise((resolve) => {
          resolveMU = resolve
        })
      }
      return Promise.resolve({
        lastUpdateId: 2,
        bids: [['500.00', '2']],
        asks: [['500.01', '3']],
      })
    })
    const { rerender } = render(
      <LanguageProvider>
        <OrderBook symbol="MUUSDT" />
      </LanguageProvider>
    )
    rerender(
      <LanguageProvider>
        <OrderBook symbol="XAUUSDT" />
      </LanguageProvider>
    )
    await waitFor(() => expect(screen.getByText('500.00')).toBeInTheDocument())
    await act(async () => {
      resolveMU({
        lastUpdateId: 1,
        bids: [['100.00', '2']],
        asks: [['100.01', '3']],
      })
      await Promise.resolve()
    })
    expect(screen.queryByText('100.00')).not.toBeInTheDocument()
    expect(screen.getByText('500.00')).toBeInTheDocument()
  })
})
