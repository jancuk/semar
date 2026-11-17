# Plan 29: Semar signs in to a web application

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 22 and 28.
* **With:** Plan 30, which needs a browser that is signed in.
* **Implements:** PRD decision D73 and FR-365 to FR-368. TRD section "Signing in to a web application".

## Goal

A person names the user and the password variable of an account once. From then on, Semar signs in by itself wherever a web step or a recording meets the sign-in form. Nobody types a password into a window of Semar, and no test holds sign-in steps.

Before this plan a recorded test started with the steps of the sign-in, because the person typed them, and each recording started with that typing. An application that keeps its session in the tab shows the form at every start, so the typing never ended. The profile of Plan 22 helps only an application that keeps its session in a cookie that lasts.

## Decisions

Each row says what was built. Where it differs from the plan as first written, the last sentence of the reason says how and why.

| # | Decision | Reason |
| --- | --- | --- |
| D1 | An auth profile may hold `web: {user: <text>, password: "env:<NAME>"}`. `password` is a secret reference of the same form as a value of `secrets`. A profile with no `web` behaves as before. Both values are needed or neither, `user` follows the rules of `description` and is not empty, and `password` must be `env:` and a variable name. The variable of the password is one of the variables that a harness of the planner never receives, as the variables of `secrets` are | The account already holds the token of the API. The web sign-in is the same account on another surface, so it belongs in the same place. The password stays in the environment. Changed: the plan did not name the harness rule. A planner must not see a password that a token rule already hides from it |
| D2 | After every `open` of a web step, when the page has settled, the engine looks for a sign-in form. When there is one and the account of the step has `web`, the engine signs in before the step ends. When there is no form, it does nothing. The account of a step is its auth profile, and `default` when the step names none | One rule serves an application that asks at every start and one that asks once a week. It needs no state, and it is safe to repeat. A form that appears after the page has settled is not seen |
| D3 | The form is found with no model, in `signInJS` of `internal/engine/web/signin.go`. The password field is the one visible `input[type=password]` of the page. Two or more, or none, is no sign-in form. The user field is the last visible, enabled text, email, or tel input before it in the document, in the same `form`, or in no form when the password field is in none. The engine types the user and the password as a `fill` step does | Almost every sign-in form has this shape, and a rule in code costs nothing and gives the same answer each time. Two password fields are a form that sets a password, which Semar must not fill. Changed: a password field with no such user field before it is no sign-in form either, because a screen that asks only for a password is a lock screen or a confirmation, and Semar has no user to give it |
| D4 | To send the form, the engine clicks the submit control of the `form`: a `button` whose type is not `button` or `reset`, or an `input[type=submit]`, visible and enabled. With no such control it clicks the first visible `button` after the password field. With no button it presses Enter in the password field | A form with a script on a plain button does not answer to Enter, and a form with no button does. Changed: a reset button is no way to send, and the second rule reads `button` elements only, as an `input[type=button]` is rare in a sign-in |
| D5 | The sign-in has worked when the page shows no sign-in form any more before the wait ends. The engine then compares the address with the one that the step opened, ignoring the fragment, and opens it again once when they differ | An application that sends a person to its home page after the sign-in would leave the test on the wrong page. Changed in two ways. The wait is a third of the time of an action, which is ten seconds with the default of thirty, and not the whole time, so that the page can still be read and photographed when the sign-in does not finish. And the test is that the page shows no sign-in form, not that the password field of the click is gone, because a form that the server draws again after a wrong password is a new password field in a new document |
| D6 | A sign-in that does not finish ends the step with an error that names the account, says that the form is still on the page, names what to check, which is `web.user` and the variable of the password, and quotes the text of the first visible element with the role `alert` when there is one. The scenario is not `failed`: the checks of the step are not evaluated | The product was not judged. The person needs the cause in the line, and the application often prints it. Changed: the line is one sentence of the form `signing in as support did not finish: the sign-in form is still on the page. Check web.user and ORDERS_WEB_PASSWORD. The page says "Wrong email or password"`. The page's own words come last and are cut at 60 characters, because a report cuts a reason at 200 characters and the account and the variable must survive. A password that cannot be read, and a field that cannot be typed, end the step the same way |
| D7 | The stored action of the `open` says that the engine signed in and as which account. It holds the user. The value of the password is given to the redactor before it is typed and is in no file, log, or screenshot text | A report must show why a page differs from the address that was opened, and a secret never reaches the store. Changed: the record is `web.signIn: {account, user}`, redacted like the other text of the record, and the activity line of the step ends `done, signed in as <account>`. The redactor learns the value when the engine resolves it, which is before the first key |
| D8 | A scenario that tests the sign-in page itself uses an account with no `web` | No new syntax. The account says who signs in and how |
| D9 | `semar record` signs in the same way after it opens the first page, and starts to record after the sign-in. The written test has no step of the sign-in. `record` prints `Signed in as <account>.` | The person does the flow of the test and nothing else. Changed: the window starts with no recorder in its pages when the account has `web`. After the sign-in, the recorder is added to every page and to the page that is open, the events so far are dropped, and the first event is an `open` of the first address, so that the written test starts where a replay starts. A sign-in that does not finish ends the command with `nothing was recorded` and the line of D6 |
| D10 | `semar init` asks, on a terminal, after each account: the user that it signs in with on the web (Enter for none), and then the variable of its password. The flags are `--web-user <account>=<user>` and `--web-password-env <account>=<NAME>`, with `default` for the first account | A new repository gets it with the other answers. Changed: an account that a flag names is not asked about, `plan` takes the two flags because it takes every flag of `init`, a file that exists is compared with them as with the other flags, and the output ends with `export <NAME>=<the web password of the account ...>` for each variable |
| D11 | `semar login <service> --user <user> --password-env <NAME> [--as <account>]` writes the `web` block of the account into `semar.yaml`, signs in once with no window, and prints the result. With neither flag, `login` opens a window as before | A repository that is set up needs one command and no editing of YAML. The trial tells the person at once that the two values are right. Changed: the block is written first and the trial comes after, so a wrong password leaves the block for the person to keep. The command changes the one line: `config.SetWebSignIn` adds the block below the last key of the account, or replaces the block that there is, and keeps every comment and every other byte. An account that is written on one line is refused, and the person adds the block by hand. A sign-in that works exits with 0. A sign-in that does not finish, a password variable that is not set, and a page with no sign-in form print why and exit with 2. A flag that is missing its partner, a name that is not a variable, and an account that the file lacks exit with 3 and write nothing |
| D12 | The machine check of `semar init` lists a password variable that is not set as `missing`, with the account that needs it | A second developer learns it before a run does. The line reads `ORDERS_WEB_PASSWORD is not set: export it with the web password of the account "default"` |
| D13 | Not built: a form in two pages, a sign-in through another site, a second factor, and a captcha. `semar login` with a window stays the way for those, where the session lasts in the profile | Each needs its own design, and the form in one page covers the common case |

## Contract

```text
$ semar init --base-url https://shop.uat.example.com --service shop \
      --web-user default=ana@example.com --web-password-env default=SHOP_WEB_PASSWORD
wrote .semar/semar.yaml
export SEMAR_TOKEN=<the token of the test account> before semar run
export SHOP_WEB_PASSWORD=<the web password of the account default>

$ semar login shop --user ana@example.com --password-env SHOP_WEB_PASSWORD
Wrote the web sign-in of default to .semar/semar.yaml.
Signed in as default (ana@example.com) at https://shop.uat.example.com. The profile of default in uat is saved.

$ semar record SHOP-482-07 --open /orders
Signed in as default.
A Chrome window is open at https://shop.uat.example.com/orders
  Do the flow. Hold Option and click what must be true. Close the window to finish.
```

The file gains one line below the last key of the account:

```yaml
authProfiles:
  default:
    session: {header: Authorization, value: "Bearer {{secret.TOKEN}}"}
    secrets: {TOKEN: "env:SEMAR_TOKEN"}
    web: {user: ana@example.com, password: "env:SHOP_WEB_PASSWORD"}
```

A wrong password in a run, as the report shows it when the `open` is a `Given` line:

```text
Outcome: **blocked**
- Reason: setup failed: open failed: signing in as default did not finish: the sign-in form is still on the page. Check web.user and SHOP_WEB_PASSWORD. The page says "Wrong email or password"
```

## Trust

| Risk | Answer |
| --- | --- |
| A password reaches a file, a log, or a stream | The engine resolves it through the resolver that feeds the redactor, so the redactor knows it before the first key. The record of the sign-in holds the user and never the password. Tests sign in with a right and a wrong password and search the store, `.semar`, the Semar home, and every output for it |
| The harness of the planner receives the password variable | The variable is in the list of the variables that a harness never gets, with those of `secrets` |
| Semar fills a form that is not a sign-in | The form must have one visible password field and a user field before it. A form that sets a password has two password fields and is left alone |
| A wrong password locks the account of the test user | A sign-in is tried once for each `open`. It is not retried. The person fixes the variable, and `semar login --user` tries it once |
| A page that the application sends after the sign-in hides what the step opened | The engine opens the address of the step again when the tab is elsewhere |
| A test of the sign-in page itself is signed in before it starts | Such a scenario uses an account with no `web` (D8) |

## As built

* **The form** is one value, `signInForm`: the user field, the password field, and the submit control, which is zero when the form is sent with Enter. `reader.signInForm` reads it with one page function, `signInJS`, in `internal/engine/web/signin.go`, and returns false when the page has no form. `search` and `signInForm` share `elementsOf`, which turns a page array into backend IDs.
* **The states** are `noSignInForm`, `signedIn`, and `signInUnfinished`. `signer.afterOpen` takes a tab, finds the form, types, sends, waits, and returns whether it signed in. A sign-in that did not finish, a password that cannot be read, and a field that cannot be typed are a `SignInError`. Another error is the browser failing.
* **Two tabs use one `signer`.** The engine's session and the window of a recording both implement `signInTab`: settle, location, and navigate. The window has no guard that counts requests, so it settles when the page is complete and its tree has not changed for a short time.
* **The engine** has `Options.Accounts`, the `web` block of each account, which `internal/cli` builds from the configuration for a run and for a journey. `Do` signs in in the `open` case, after the page has loaded and before it settles for the read, and stores `Web.SignIn`.
* **The recorder** has `RecorderOptions.SignIn`. With it the window starts with no recorder, `Open` signs in, and `arm` adds the recorder. `SignedIn` says whether it signed in. `web.SignIn` uses the same window with no screen for `semar login --user`.
* **The file** is changed by `config.SetWebSignIn`, which edits lines and not the tree, so that a comment, a flow style, and every other byte stay. It checks that the result loads.
* **`init`** keeps `webUsers` and `webPasswordEnvs` in `initFlags`. `parseWebSignIns` pairs them and refuses a user without a variable, a variable without a user, an account that is not named, and a name given twice.

### Tests

The tests of the form and of the engine run a real Chrome with no window against a local page, and skip when Chrome is absent. Each one uses a temporary Semar home, `keychain.Memory()`, and never the profile that a person browses with.

* `internal/config`: the block and each way to refuse it, and `SetWebSignIn` with comments kept, a block replaced, the same block written twice, an account on one line, and an account that is not there.
* `internal/engine/web`: the form on fourteen pages (a plain form, a script on a `type=button` button, an `input[type=submit]`, a form with no button, no `form` element, the last user field, a hidden user field, the field of another form, a reset button, two password fields, a hidden password field, one hidden and one visible, a password with no user, and no form). The engine on a page that keeps its session in `sessionStorage` with two scenarios of one run, a page that sends the tab elsewhere, a wrong password with an alert, an account with no `web`, an account that is not the one of the step, a page with no form, and the password searched for in the action and in every file of the store, for a right and a wrong password.
* `internal/cli`: a recording on a page that keeps its session in the tab writes no step of the sign-in, prints `Signed in as`, and replays as `passed`. Two recorded scenarios of one run both pass. A wrong password gives a run that is `blocked` and a report that names the account, the variable, and the alert. `init` with the questions, the flags, the refusals, and the machine check. `login` with a right password, a wrong one, a variable that is not set, and flags that are refused.

## Not built

* A form in two pages, a sign-in through another site, a second factor, and a captcha. `semar login` with a window is the way, where the session lasts in the profile.
* A form that appears after the page has settled, and a form in a frame.
* A sign-in on Android.
* Sign-in steps that a person writes. The account says how to sign in, and a scenario never repeats it.
* A user field that the form lacks, such as a screen that asks for a password only.
