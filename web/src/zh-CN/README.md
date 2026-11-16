# Chinese UI supplement

The web tests run in the English UI (vitest project `en-US`). The tests in this directory run the same console in the
Chinese UI (project `zh-CN`, set up by `src/test/locale-zh.ts`; see `vitest.config.ts`). They cover what the English
suite cannot:

- choosing and switching the UI language;
- behaviour that differs by language (date, time and number formats, joins with Chinese punctuation);
- text from older servers and older stored data that is still Chinese;
- the Chinese copy table itself (shape and parameterized copy rendered in Chinese);
- a smoke test per main page that the page renders its key Chinese text.

Everything else belongs in the main suite, in English. Chinese strings outside this directory need a
`check-english: zh (reason)` marker (see `scripts/check-comments.mjs`).
