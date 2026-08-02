# 本地静态资源（图标与字体）
图标在 `public/icons/`。
字体在 `public/fonts/`。
两者都随构建原样落到产物根目录下。

## 来源与许可

| 文件 | 来源 | 许可 |
|---|---|---|
| `*-fill.svg` | [Remix Icon](https://remixicon.com/) 的 `ri` 图标集，经 iconify 导出并统一填成 `#888888` | Apache License 2.0 |
| `feishu-lark.svg` | 飞书 / Lark 品牌标识 | 版权归飞书所有，仅用于标示对应渠道 |
| `bark.png` | Bark 应用图标 | 版权归 Bark 作者所有，同上 |

品牌标识按「指代该产品」的合理使用放在这里。
不做任何背书暗示。
换图标时保持 `#888888` 这个灰度。
列表里所有单色图标都对齐到同一个灰阶。
换成纯黑或品牌色会在两栏网格里跳出来。

## 添加渠道图标

1. 把图标放进 `public/icons/`。
   文件名与 `CH_DEFS` 里的 `id` 对得上即可（不强制）。
2. 在 `frontend/src/lib/channels.ts` 里把 `icon` 写成 `/icons/你的文件名`。
3. 运行 `pnpm smoke`。
   `frontend/src/lib/channels.test.ts` 有一条断言会检查渠道图标不是外链。

## 字体

`public/fonts/` 里是 Instrument Sans 与 JetBrains Mono 的 woff2。
各两个子集，合计约 84 KB。
两者都是可变字体。
一份文件覆盖整段字重。
所以不按字重分文件。
`@font-face` 写在 `src/styles/fonts.css`。
该文件也解释了中文为什么不下发字体文件。

| 字体 | 许可 |
|---|---|
| Instrument Sans | SIL Open Font License 1.1 |
| JetBrains Mono | SIL Open Font License 1.1 |

换版本：从 Google Fonts 重新下载同名 woff2，并同步 `fonts.css` 里的 `unicode-range`。
