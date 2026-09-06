import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { trafficBodyPresentation, TrafficTextContent } from './TrafficFormatting'

describe('traffic JSON presentation', () => {
  it('indents nested JSON while retaining duplicate keys, key order and exact tokens', () => {
    const body = String.raw` {"10":9007199254740993,"2":1.00,"x":-0,"x":1E+400,"nested":[{},[],{"escaped":"\u0061\n\"{}[],:"}]} `
    expect(trafficBodyPresentation(body)).toEqual({ json: true, text: String.raw`{
  "10": 9007199254740993,
  "2": 1.00,
  "x": -0,
  "x": 1E+400,
  "nested": [
    {},
    [],
    {
      "escaped": "\u0061\n\"{}[],:"
    }
  ]
}` })
  })

  it.each(['application/json; charset=utf-8', 'application/problem+json', 'TEXT/JSON'])('formats declared JSON scalars for %s', (contentType) => {
    expect(trafficBodyPresentation(' 9007199254740993 ', contentType)).toEqual({ text: '9007199254740993', json: true })
  })

  it.each(['{"unfinished":', '{"trailing":true} false', '{"invalid":01}', 'unstructured text\nwith spacing  ', '123'])('preserves plain text and malformed JSON verbatim: %s', (body) => {
    expect(trafficBodyPresentation(body)).toEqual({ text: body, json: false })
  })

  it('falls back to the captured text when nesting or formatting expansion is excessive', () => {
    for (const body of ['['.repeat(101) + '0' + ']'.repeat(101), '['.repeat(99) + Array(5500).fill('0').join(',') + ']'.repeat(99)]) {
      expect(trafficBodyPresentation(body)).toEqual({ text: body, json: false })
    }
  })

  it('renders exact values with trace syntax colors and escapes HTML content', () => {
    const html = renderToStaticMarkup(<TrafficTextContent content={'{"id":9007199254740993,"x":1.00,"x":-0,"text":"<script>alert(1)</script>","ok":true,"none":null}'} />)
    expect(html).toContain('class="traffic-json__number">9007199254740993</span>')
    expect(html).toContain('class="traffic-json__number">1.00</span>')
    expect(html).toContain('class="traffic-json__number">-0</span>')
    expect(html.match(/class="traffic-json__key">&quot;x&quot;/g)).toHaveLength(2)
    expect(html).toContain('class="traffic-json__string">&quot;&lt;script&gt;alert(1)&lt;/script&gt;&quot;</span>')
    expect(html).toContain('class="traffic-json__boolean">true</span>')
    expect(html).toContain('class="traffic-json__null">null</span>')
    expect(html).not.toContain('<script>')
  })
})
