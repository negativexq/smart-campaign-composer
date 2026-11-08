# Signal

The design language of Smart Campaign Composer: **calm, precise, human-controlled.**

Signal is the internal name of this design language, not a product name, so it does not appear in the product UI outside the design review page. Tokens live in [`frontend/src/design/tokens.css`](frontend/src/design/tokens.css). Run the app and open `/design` to see every component with sample data.

**Design principle:** make the next correct action obvious.

## Identity

A focused professional tool, not an AI chatbot or a generic analytics template. References are Klaviyo's content focus, Customer.io's structure and Linear's visual discipline. None of them is copied.

## Color

| Token | Value | Use |
|---|---|---|
| `--color-canvas` | `#F7F8FA` | Page background |
| `--color-surface` | `#FFFFFF` | Cards, inputs, navigation |
| `--color-surface-subtle` | `#F0F3F8` | Quiet fills |
| `--color-border` | `#E4E8EF` | Card borders and dividers |
| `--color-border-input` | `#858FA1` | Input borders, 3:1 against the surface |
| `--color-text` | `#172033` | Primary text |
| `--color-text-muted` | `#5B6A80` | Secondary text |
| `--color-primary` | `#315BE8` | Primary actions, active steps, key selections, and a light tint on info notes |
| `--color-primary-soft` | `#EAF0FF` | Active navigation fill |
| `--color-ai` | `#6658B8` | Small AI marker only, never a large area |
| `--color-success` | `#047857` | Success |
| `--color-warning` | `#B45309` | Warning, on surface or canvas only |
| `--color-error` | `#DC2626` | Error, on surface or canvas only |

Color is semantic and never the only signal: every status also has a text label and an icon. WCAG AA contrast is enforced by `frontend/src/design/tokens.test.ts`.

The first version was light only. Settings now offers light, dark and the device's choice,. The contrast test covers the light tokens only.

## Typography

| Role | Style |
|---|---|
| Page title | 28px, semibold |
| Section title | 20px, semibold |
| Card title | 16px, semibold |
| Body | 14px, regular |
| Helper text | 12 to 13px |

Inter is the UI face. Metrics use Inter with tabular numbers. JetBrains Mono is reserved for technical content: variables, keys, counters and timestamps.

## Shape and layout

- Spacing is based on 4px: 8, 12, 16, 24 and 32.
- Cards use a 12px radius and a 1px border. Buttons and inputs use 8px.
- Shadows are for overlays and dropdowns only.
- Inputs and buttons are at least 40px tall.
- Icons are Lucide outlines at 14 to 20px.
- Maximum canvas width is 1440px, the sidebar is 216px and the top bar is 64px.
- The preview panel is 320 to 360px and moves below the editor under 1200px.

## AI behavior

- AI suggestions are marked and stay distinguishable from user edits. That label names no vendor, because the provider is configurable. The top bar's badge is different: it shows the setup, so it names the model in use, or `Mock AI`.
- Streaming content is a draft. `Validated` appears only after the backend confirms it.
- Assumptions and conditions the system cannot apply are shown, never dropped silently.
- Audience conditions come before counts in reading order. A plausible count does not prove the conditions are right.
- Errors say what needs to change.
- Every consequential action needs a clear human decision. The approval button states exactly what will be sent.

## Campaign flow

Brief → Audience → SMS → Email → Review → Monitor.

The steps up to Review form the creation workspace, and a channel the campaign does not use has no step. Monitor becomes the active view after dispatch. The UI always shows the campaign status, the current step and the next available action.

## Operational states

| Database status | Label | Meaning |
|---|---|---|
| `QUEUED` | Queued | Waiting for its turn |
| `SENDING` | Sending | An attempt is in progress |
| `SENT` | Accepted | The mock provider accepted it. This does not confirm delivery to a person |
| `RETRYING` | Retrying | A temporary error occurred and another attempt is scheduled |
| `FAILED` | Failed | Rejected, or attempts ran out |
| `SKIPPED` | Skipped | Not sent, for example because consent was withdrawn |
| `UNKNOWN` | Outcome unknown | Attempts ended on a timeout, so the result cannot be known |

## Interaction and accessibility

- Visible keyboard focus and real labels on every field.
- Motion lasts 120 to 200ms and respects the reduced-motion preference.
- Streaming updates never move keyboard focus, and counters do not animate.
- A zero-recipient campaign cannot be approved.

## Components

The core components are `CampaignStepper`, `BriefComposer`, `AudienceRule`, `MetricCard`, `MessageEditor`, `PhonePreview`, `ValidationNotice`, `ApprovalSummary` and `DeliveryTimeline`, supported by `AppShell`, `OriginTag` and `StatusBadge`. Later pages, such as the calendar, the template editor and notifications, are built from the same tokens.

## Exclusions

No chatbot-first layout, journey canvas, large gradients, glass effects, decorative AI particles, meaningless charts or floating cards without a purpose.
