/** 功能：验证目标凭据 DTO 和范围校验；实现：投影未知响应并检查目标 URL；输入：含敏感冗余字段的响应；输出：安全字段和固定错误。 */
import { describe, expect, it } from 'vitest'
import { normalizeCredentialOrigin, parseCredential, targetURLsMatch } from './api'

const safe = { id: 'credential-1', name: '推理服务', origin: 'https://inference.example.com', allow_insecure_http: false, auth_type: 'bearer', header_name: '', revision: 1, disabled: false, created_at: '2026-09-07T00:00:00Z', updated_at: '2026-09-07T00:00:00Z' }
describe('目标凭据安全 DTO', () => {
  it('HTTP 源默认可用，并使用实际协议默认端口', () => {
    expect(normalizeCredentialOrigin('http://inference.example.com:80/')).toBe('http://inference.example.com')
    expect(normalizeCredentialOrigin('http://inference.example.com:80/')).toBe('http://inference.example.com')
    expect(normalizeCredentialOrigin('HTTP://INFERENCE.EXAMPLE.COM:443/')).toBe('http://inference.example.com:443')
    expect(normalizeCredentialOrigin('https://inference.example.com:443/')).toBe(safe.origin)
    expect(normalizeCredentialOrigin('https://inference.example.com:80/')).toBe(`${safe.origin}:80`)
    for (const origin of ['ftp://inference.example.com', 'http://u:p@inference.example.com', 'http://inference.example.com/path', 'http://inference.example.com?query=1', 'http://inference.example.com#fragment']) expect(normalizeCredentialOrigin(origin)).toBeUndefined()
  })
  it('HTTP DTO 按实际协议解析，并兼容旧许可字段', () => {
    const http = { ...safe, origin: 'http://inference.example.com', allow_insecure_http: true }
    expect(parseCredential(http)).toEqual(http)
    for (const allow of [undefined, false]) expect(parseCredential({ ...http, allow_insecure_http: allow })).toEqual(http)
    for (const allow of ['true', 1, null]) expect(() => parseCredential({ ...http, allow_insecure_http: allow })).toThrow()
    expect(parseCredential({ ...safe, allow_insecure_http: undefined })).toEqual(safe)
    expect(parseCredential({ ...safe, allow_insecure_http: true })).toEqual(safe)
    expect(() => parseCredential({ ...safe, allow_insecure_http: 'true' })).toThrow()
  })
  it('HTTP 同源匹配仍拒绝不同协议、主机、端口和发现表达式', () => {
    const origin = 'http://inference.example.com'
    expect(targetURLsMatch(origin, `${origin}/api/version`)).toBe(true)
    expect(targetURLsMatch(origin, `${origin}/a\n${origin}:80/b?version=1`)).toBe(true)
    expect(targetURLsMatch('http://inference.example.com:443', 'http://inference.example.com:443/api')).toBe(true)
    for (const target of ['inference.example.com', 'https://inference.example.com', 'http://inference.example.com:443', 'http://other.example.com', 'http://u:p@inference.example.com', 'http://inference.example.com#fragment', '192.0.2.*']) expect(targetURLsMatch(origin, target)).toBe(false)
    expect(targetURLsMatch(safe.origin, origin)).toBe(false)
  })
  it('去除服务端冗余秘密字段后才能缓存', () => {
    expect(parseCredential({ ...safe, secret: 'response-sentinel', username: 'sensitive-user', encrypted_secret: 'ciphertext' })).toEqual(safe)
  })
  it('拒绝不合法目标和版本', () => {
    expect(() => parseCredential({ ...safe, revision: 0 })).toThrow()
    expect(() => parseCredential({ ...safe, origin: 'https://u:p@example.com' })).toThrow()
  })
  it('只允许明确的同源 HTTPS URL', () => {
    expect(targetURLsMatch(safe.origin, `${safe.origin}/a\n${safe.origin}:443/b`)).toBe(true)
    for (const target of ['inference.example.com', 'http://inference.example.com', 'https://inference.example.com:8443', 'https://other.example.com', 'https://u:p@inference.example.com']) expect(targetURLsMatch(safe.origin, target)).toBe(false)
  })
  it('接受服务器 IPv4-mapped IPv6 源并按浏览器规范比较目标', () => {
    const origin = 'https://[::ffff:127.0.0.1]'
    expect(parseCredential({ ...safe, origin }).origin).toBe(origin)
    expect(targetURLsMatch(origin, 'https://[::ffff:7f00:1]/api/version')).toBe(true)
    expect(targetURLsMatch(origin, 'https://[::ffff:127.0.0.2]/api/version')).toBe(false)
  })
})
