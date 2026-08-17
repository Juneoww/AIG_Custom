# 控制台字体资产清单

本目录只保存 IBM Plex Sans 的拉丁文字体。中文正文复用仓库已有的 Droid 字体，构建时复制到 ignored 的 `.generated/fonts/`，避免重复提交约 4 MiB 二进制。

`prepare:fonts` 只发布固定白名单：三份字体进入 `.generated/fonts/`，IBM OFL、Droid 归属说明和完整 Apache-2.0 正文进入 `.generated/licenses/`。Vite 构建后对应文件位于 `dist/fonts/` 与 `dist/licenses/`；许可文本从现有仓库文件复制，不重复维护副本。

## IBM Plex Sans

- 官方仓库：`https://github.com/IBM/plex`
- 发布标签：`@ibm/plex-sans@1.1.0`
- 标签对象：`036e2d2a727bd6a378f69b7cde8d8dd20c472dae`
- 固定提交：`1da12f02587b630c07e92692d21492d722f53614`
- Regular 源：`packages/plex-sans/fonts/complete/woff2/IBMPlexSans-Regular.woff2`
- Regular 字节数：`63020`
- Regular SHA-256：`ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a`
- SemiBold 源：`packages/plex-sans/fonts/complete/woff2/IBMPlexSans-SemiBold.woff2`
- SemiBold 字节数：`67060`
- SemiBold SHA-256：`f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205`
- 许可源：提交根目录 `LICENSE.txt`
- 上游许可字节数：`4456`
- 上游许可 SHA-256：`7e6b2818edbd8f6a01ae80641cc8f16a51080d08fb4e532be3a0b6f74adb07da`
- 仓库许可规范化：仅将 CRLF 转为 LF 并移除一处行尾空格，不改变许可文字
- 仓库许可字节数：`4362`
- 仓库许可 SHA-256：`d741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f`
- 许可：SIL Open Font License 1.1，完整文本见 `IBM_PLEX_LICENSE.txt`

## Droid Sans Fallback

- 官方来源：Android Open Source Project
- 发布标签：`android-11.0.0_r26`
- 源路径：`frameworks/base/data/fonts/DroidSansFallbackFull.ttf`
- 固定 Gitiles blob：`68641aad1d1b02922fc1ca61fdcd119cb051ef06`
- 仓库源文件：`internal/platform/reports/assets/DroidSansFallbackFull.ttf`
- 字节数：`4033576`
- SHA-256：`2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240`
- 许可：Apache License 2.0，归属见 `internal/platform/reports/assets/DROID_FONT_LICENSE.txt`
- 发布归属：`.generated/licenses/DROID_FONT_LICENSE.txt`
- 完整许可：仓库根 `LICENSE`，发布为 `.generated/licenses/APACHE-2.0.txt`
