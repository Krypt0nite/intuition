# Content guide

Every question should make the player say one of: *"Oh, obviously."* · *"Wait… really?"* · *"I never thought about that."* · *"I was completely wrong."* If it does none of these, rewrite or drop it.

The game tests **intuition, not memory**. A player who knows no facts should still be able to reason toward an answer.

| Bad | Good |
| --- | --- |
| What is the boiling point of water? | You climb a very high mountain. Does water boil at a higher or lower temperature? |
| What is the speed of light? | You switch on a flashlight on a ship moving at 90% of light speed. How fast does the beam leave you? |

## Where content lives

```
content/
├── taxonomy.json            categories (id, name, emoji, prefix, colour, subcategories), question & answer types
├── <category>/<pack>.json   question packs
├── daily/schedule.json      optional curated daily sets: {"days": {"2026-12-31": [5 ids]}}
└── schema/question.schema.json   editor autocompletion
```

A pack has shared `defaults` applied to every question that doesn't override them:

```json
{
  "pack": "physics-optics-2",
  "defaults": { "category": "physics", "visualType": "emoji", "reviewStatus": "draft", "source": "OpenStax University Physics" },
  "questions": [ { ... } ]
}
```

Unknown fields are rejected, so typos like `explaination` fail validation.

## Field reference

| Field | Required | Notes |
| --- | --- | --- |
| `id` | ✓ | kebab-case, starts with the category prefix (`phy-`, `chem-`, `bio-`, `astro-`, `earth-`, `every-`, `tech-`, `math-`, `perc-`). Never reuse or rename once shipped: player history is keyed by it. |
| `category`, `subcategory` | ✓ | Must exist in `taxonomy.json`. Add new subcategories there first. |
| `difficulty` | ✓ | `easy` · `medium` · `hard` · `expert` (rubric below) |
| `questionType` | ✓ | prediction, comparison, direction, ordering, true-false, magnitude, time, multi-stage, chain, estimation, causal |
| `answerType` | ✓ | change, direction, comparison, time, causal, boolean, choice, magnitude, quantity |
| `scenario` | ✓ | The situation, written so the player can picture it. Second person, present tense. |
| `question` | ✓ | One question, ending in `?` |
| `options` | ✓ | 2–4 short, parallel options |
| `correctIndex` | ✓ | 0-based index into `options` |
| `shuffle` | | Default `true`. Set `false` for ordered scales (*none / half / nearly all*) so they display in order. |
| `reveal` | ✓ | The one-line answer headline, under 100 characters |
| `explanation` | ✓ | What happened and why. 50–120 words is the target; 25–220 is enforced. |
| `intuitionTrap` | ✓ | The specific instinct that misleads people |
| `principle` | ✓ | The underlying rule in one sentence. Shown highlighted. |
| `funFact` | strongly encouraged | The "Wait, really?" moment |
| `caveat` | when needed | Where the simple answer stops being true |
| `assumptions` | when needed | e.g. "Ignoring air resistance". Shown with the explanation. |
| `goDeeper` | optional | `{equation, text, example}` for the expandable section |
| `tags` | ✓ | kebab-case, free-form |
| `concepts` | ✓ | kebab-case concept ids. The selector spaces out shared concepts and tracks mastery per concept, so **reuse existing ids** where the idea is the same (`make content-stats` lists them). |
| `variantGroup` | ✓ | Questions testing the *same intuition* share a group; never two in one session. |
| `chain` | optional | `{id, step, total}`; steps are always served consecutively (sessions of 8+) |
| `source`, `sourceUrl` | encouraged | Not shown during play except in "Go deeper". Only cite what you have checked. |
| `reviewStatus` | ✓ | `draft` (never served in production) → `reviewed` → `verified` |
| `reviewNotes` | optional | Who checked what |

## Writing the scenario

- Talk like a curious person, not a textbook: *"You're standing in an elevator. It suddenly speeds up as it heads upward."*
- Make the setup unambiguous. If the answer depends on a condition, state it in the scenario ("engines off", "a loose robe", "an ideal gas") rather than hiding it in a caveat.
- Specify **the moment** you're asking about. "What happens to the pressure in a sealed bottle in the freezer?" has different answers while cooling and after freezing; ask about one.
- No jargon the player needs to already know. If a term is needed, the scenario defines it.

## Writing options

- Short and parallel in grammar and length. The validator warns when the correct option is much longer than the distractors.
- Every distractor should be something a reasonable person believes. The best distractor *is* the intuition trap.
- No "all of the above" (options are shuffled).
- Don't leak the answer's wording into the scenario.

## Writing the reveal

Answer these, in this order: what happened; why; which instinct misled you; the principle; any caveat. Then add the fun fact. Use an equation only in `goDeeper`, and only if it genuinely clarifies.

## Difficulty rubric

Difficulty is about reasoning, not obscurity.

| Level | The player needs to… | Example |
| --- | --- | --- |
| easy | apply everyday experience correctly | Elevator speeding up: heavier or lighter? |
| medium | apply one scientific idea | Same mass, double radius: what happens to your weight? |
| hard | combine ideas or resist a strong trap | Rock thrown out of a boat: does the pond level rise or fall? |
| expert | overturn a confident, common belief | Gravity 2,000 km down inside Earth is about the same as at the surface |

The selector serves mostly easy/medium to new players and shifts toward hard/expert as they answer more and score well, so every level needs a healthy supply.

## Accuracy checklist (every question)

1. Exactly one option is correct under the stated conditions.
2. The explanation is true, not just the answer. Watch for oversimplifications that become false.
3. Every number is checked (run the arithmetic; cite where a number comes from).
4. Assumptions are stated where the answer depends on them.
5. The caveat covers the obvious "well, actually".
6. It's not a viral myth. Several famous "facts" are wrong (10:1 bacteria ratio, glass flowing, Coriolis sinks); check primary sources.
7. It tests reasoning, and the wording is unambiguous to a non-expert.
8. It isn't a near-duplicate of an existing question (the validator checks, but think about intent too).

## Validation

```bash
make validate-content       # or: cd backend && go run ./cmd/content validate [-strict] [-json]
```

Errors (block CI, the Docker build and server start): missing or unknown fields, bad ids/prefixes, unknown category/subcategory/types, option count, duplicate options, out-of-range `correctIndex`, explanation under 25 or over 220 words, placeholder text (TODO, lorem…), duplicate ids, broken chains, invalid schedule entries, and **likely duplicates** (similar normalised wording plus shared concepts, or near-identical wording) in different variant groups.

Warnings (`-strict` makes them errors): explanation outside 45–130 words, long reveal headline, missing `funFact`, question without `?`, long options, correct option much longer than distractors, answer wording leaking into the question, and very similar questions *within* one variant group.

Duplicate detection normalises text (case, punctuation, stopwords, "doubles/twice/double" → one token, light stemming) and compares candidates found through an inverted index of shared concepts and rare words, so it stays fast at tens of thousands of questions.

## Scaling to thousands

The engine needs no changes to go from 150 to 10,000 questions. Content production does. A workable loop:

1. **Plan coverage**, not count: list concepts per subcategory from the taxonomy and target gaps (`make content-stats`).
2. **Draft** in batches with `reviewStatus: "draft"` (by hand or with an LLM, using this guide as the brief). Drafts are validated but never served in production.
3. **Validate** and fix all errors and warnings.
4. **Review**: a second person runs the accuracy checklist and sets `reviewed`, with notes.
5. **Verify**: a domain expert checks the trickiest claims and sets `verified`.
6. **Playtest**: watch per-question accuracy in the `answers` table. Questions everyone gets right are too easy for their level; questions near-random may be ambiguous rather than hard.

About 2,000 questions supports a dedicated daily player (30 a day) for two months without repeats.
