# Plan 29: Semar signs in to a web application

* **Status:** Planned.
* **Needs first:** Plans 22 and 28.
* **With:** Plan 30, which needs a browser that is signed in.
* **Implements:** PRD decision D73 and FR-365 to FR-368.

## Goal

A person names the user and the password variable of an account once. From then on, Semar signs in by itself wherever a web step or a recording meets the sign-in form. Nobody types a password into a window of Semar, and no test holds sign-in steps.

Today a recorded test starts with the steps of the sign-in, because the person typed them, and each recording starts with that typing. An application that keeps its session in the tab shows the form at every start, so the typing never ends. The profile of Plan 22 helps only an application that keeps its session in a cookie that lasts.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | An auth profile may hold `web: {user: <text>, password: "env:<NAME>"}`. `password` is a secret reference of the same form as a value of `secrets`. A profile with no `web` behaves as today | The account already holds the token of the API. The web sign-in is the same account on another surface, so it belongs in the same place. The password stays in the environment |
| D2 | After every `open` of a web step, when the page has settled, the engine looks for a sign-in form. When there is one and the account of the step has `web`, the engine signs in before the step ends. When there is no form, it does nothing | One rule serves an application that asks at every start and one that asks once a week. It needs no state, and it is safe to repeat |
| D3 | The form is found with no model. The password field is the one visible `input[type=password]` of the page; two or more, or none, is no sign-in form. The user field is the last visible text, email, or tel input before it in the document, inside the same `form` when there is one. The engine types the user and the password with key events, as a step does | Almost every sign-in form has this shape, and a rule in code costs nothing and gives the same answer each time. Two password fields are a form that sets a password, which Semar must not fill |
| D4 | To send the form, the engine clicks the submit control of the `form` (a `button` that is not `type=button`, or `input[type=submit]`). With no such control it clicks the first visible button after the password field. With no button it presses Enter in the password field | A form with a script on a plain button does not answer to Enter, and a form with no button does |
| D5 | The sign-in has worked when the password field is gone before the action times out. The engine then compares the address with the one that the step opened, and opens it again once when they differ | An application that sends a person to its home page after the sign-in would leave the test on the wrong page |
| D6 | A sign-in that does not finish ends the step with an error that names the account, says that the form is still there, quotes the text of the first visible element with the role `alert` when there is one, and names what to check: `web.user` and the variable of the password. The scenario is not `failed` | The product was not judged. The person needs the cause in the line, and the application often prints it |
| D7 | The stored action of the `open` says that the engine signed in and as which account. It holds the user. The value of the password is given to the redactor before it is typed and is in no file, log, or screenshot text | A report must show why a page differs from the address that was opened, and a secret never reaches the store |
| D8 | A scenario that tests the sign-in page itself uses an account with no `web` | No new syntax. The account says who signs in and how |
| D9 | `semar record` signs in the same way after it opens the first page, and starts to record after the sign-in. The written test has no step of the sign-in. `record` prints `Signed in as <account>.` | The person does the flow of the test and nothing else |
| D10 | `semar init` asks, on a terminal, after each account: the user that it signs in with on the web (Enter for none), and then the variable of its password. The flags are `--web-user <account>=<user>` and `--web-password-env <account>=<NAME>`, with `default` for the first account | A new repository gets it with the other answers |
| D11 | `semar login <service> --user <user> --password-env <NAME> [--as <account>]` writes the `web` block of the account into `semar.yaml`, signs in once with no window, and prints the result. With neither flag, `login` opens a window as before | A repository that is set up needs one command and no editing of YAML. The trial tells the person at once that the two values are right |
| D12 | The machine check of `semar init` lists a password variable that is not set as `missing`, with the account that needs it | A second developer learns it before a run does |
| D13 | Not built: a form in two pages, a sign-in through another site, a second factor, and a captcha. `semar login` with a window stays the way for those, where the session lasts in the profile | Each needs its own design, and the form in one page covers the common case |

## Tasks

Each task starts with a test that fails, and ends with one commit.

1. **Config.** `web` in an auth profile, its validation (both values or neither, `password` a secret reference, the same text rules as `description` for `user`), and its reading by the environment code. Tests in `internal/config`.
2. **Find the form.** A function of the web engine that reads the page and returns the user field, the password field, and how to send, or says that there is no form (D3, D4). Tests in `internal/engine/web` against pages of a test server: a plain form, a form with a script on a `type=button` button, a form with no button, a page with two password fields, a page with a hidden password field, a page with no form.
3. **Sign in after open.** D2, D5, D6, D7 in the engine. Tests: a page that keeps its session in `sessionStorage` (two scenarios of one run both sign in), a page that redirects after the sign-in, a wrong password with an `alert`, an account with no `web`, a page with no form, and that the password is in no stored action.
4. **Recorder.** D9. Test in `internal/cli`: a recording on the session page writes no sign-in step, and its replay passes.
5. **`init`.** D10 and D12. Tests of the questions, the flags, and the machine check.
6. **`login`.** D11. Tests: the file gains the block and keeps its other lines and comments, a right password prints the success, a wrong one prints the failure and exits 2, and the plain `login` is as before.
7. **Docs.** This file as built, the README, the PRD (D73, FR-365 to FR-368), and the TRD section on web steps.

## Done when

* In a repository whose default account has `web`, `semar record <id> --open /` opens the window already signed in, and the written scenario has no sign-in step.
* Two recorded scenarios of one run both pass against an application that keeps its session in the tab.
* A wrong password gives a line that names the account and the variable, and the outcome is not `failed`.
* `go test ./...` passes.
