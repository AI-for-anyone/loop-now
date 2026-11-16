# Chinese UI supplement

The web tests run in the English UI (vitest project `en-US`, set up by `src/test/locale-en.ts`). The tests in this directory
run the same console in the Chinese UI (project `zh-CN`, set up by `src/test/locale-zh.ts`; see `vitest.config.ts`). They
cover what the English suite cannot, and nothing else: every behaviour that does not depend on the language is tested once,
in English, next to its code. A file here mirrors the path of the code it tests and ends in `.zh.test.ts(x)`.

## What is covered, and where

- **Choosing and switching the UI language**
  - the console picks zh-CN from a Chinese browser: `app/console.zh.test.tsx`;
  - the language picker shows 中文 as current, choosing English saves it and reloads: `app/shell.zh.test.tsx`,
    `features/settings/SettingsPage.zh.test.tsx` (the English side of both lives in the main suite; how `?lang=`, the saved
    choice and the browser language are resolved is a pure function tested in `src/i18n/i18n.test.ts`).
- **Behaviour that differs by language**
  - date, time and number formats and Chinese units (秒, 分, 小时, 万): `i18n/zh-table.zh.test.ts`;
  - joins with Chinese punctuation (、, ：, （）) in copy functions: `i18n/zh-table.zh.test.ts`, the deleted sender's
    full-width brackets in `features/chat/InboxReminder.zh.test.tsx`, the error code copy joined with the server detail in
    `lib/errors.zh.test.ts`;
  - the chat input writes Enter as 回车, only there: `features/chat/Composer.zh.test.tsx`;
  - footnotes use the UI copy instead of the Markdown renderer's built-in English: `features/chat/Markdown.zh.test.tsx`;
  - names the console gives itself (console, platform, deleted parties) whatever the server sends:
    `features/inbox/participants.zh.test.ts`;
  - per-language plugin names and descriptions with their fallbacks: `features/plugins/Markets.zh.test.tsx`;
  - per-language labels written by plugin and CLI template authors (form titles, descriptions, options, field labels):
    `features/plugins/localized-labels.zh.test.tsx`;
  - provider search by Chinese name: `features/project-credentials/ProviderPicker.zh.test.tsx`.
- **Text from older servers and older stored data that is still Chinese**
  - an older server's fixed `接口不存在` (no such endpoint): `api/backend-signals.zh.test.ts`,
    `features/project-credentials/CLIConnectionVerification.zh.test.tsx`, `features/tasks/tasks.zh.test.tsx`;
  - the Chinese sleep wake-up sentence of sessions from before the switch to English: `features/sleep/wake-text.zh.test.ts`,
    `features/tasks/TaskMessageBubble.zh.test.tsx`;
  - error messages of task turns stored before the switch, shown as they are: `features/tasks/TaskMessageBubble.zh.test.tsx`,
    `features/tasks/TaskRunRecord.zh.test.tsx`;
  - an older server sending the Chinese copy of an error code is recognized as repeating the code: `lib/errors.zh.test.ts` (and,
    in the English UI, `src/lib/errors.test.ts`).
- **The Chinese copy table itself**: `i18n/zh-table.zh.test.ts` checks that it is the table in use, its error codes and a batch
  of parameterized copy rendered in Chinese. Its keys and the number of arguments of its functions match the English table: the
  type requires it (`Messages` is built from the Chinese table and the English one `satisfies` it), and `src/i18n/i18n.test.ts`
  compares the two key by key, including the loose maps the type cannot pin down.
- **One smoke test per main page** that it renders its Chinese copy: `app/console.zh.test.tsx` (sidebar, overview, chat, Agent
  config, plugin market, global settings, tasks, project folder, project settings, Skills library, stats, external identities).

## Rules

Everything else belongs in the main suite, in English. Chinese strings in test files outside this directory fail
`npm run check:text` (`scripts/check-test-strings.mjs`) unless the data itself must be Chinese or multibyte (a CJK name,
truncation of multibyte text, a check that asserts both languages); such a line ends with a
`// check-english: zh (<reason>)` comment, the marker the Go check uses too. Comments are English everywhere, here as well;
Chinese UI text in a comment is quoted (“…”).
