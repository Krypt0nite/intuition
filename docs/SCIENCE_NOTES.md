# Science notes on the brief's example questions

The brief asked for accuracy over viral simplifications. A few of its example situations would have taught something false or ambiguous as written, so the shipped versions differ.

## Sealed bottle of water in the freezer

The brief's sample reveal was "Roughly the same, until freezing begins." That isn't right for a typical bottle with an air gap. While cooling from room temperature to 0 °C, the trapped air's pressure drops by roughly 10% (cooler gas, much less water vapour, and more air dissolving into the colder water); squeezable bottles visibly dent. Once freezing starts, water expands about 9%, which is usually more than the headspace can absorb, so pressure rises sharply and bottles bulge or crack. The answer depends on the moment you ask about, so the question needs to name it. **Shipped instead:** the sealed soda can left overnight (`chem-soda-in-freezer`), whose explanation covers both stages.

## Fizzy drink in the freezer

Same two-stage issue: the pressure first falls slightly, then climbs as ice forms and pushes CO₂ out of the freezing liquid. The shipped question asks what's most likely to have happened by morning (it bulges or bursts).

## Jumping inside a spaceship far from any planet

Ambiguous unless the engines are off. A ship under thrust feels like gravity and you'd land again. The scenario now states "coasting, engines off" and a caveat covers the thrusting case.

## Double the radius vs. "a planet with twice Earth's radius"

These are the same question (both give ¼ gravity). Only one ships (`phy-planet-radius-weight`); the validator's duplicate test uses this exact pair. A genuinely different gravity intuition replaced the second: gravity 2,000 km inside the real Earth stays about the same rather than falling, because of the dense core (`phy-gravity-deep-inside-earth`).

## Cutting a starfish

Species-dependent. Most sea stars regrow arms; only some (e.g. *Linckia*) can regrow a whole animal from a lone arm. The answer is "in some species", not a blanket yes.

## The Sun disappearing

Physically impossible as stated (mass can't vanish), so it's framed as a thought experiment with that caveat. The conclusion (Earth keeps orbiting for about 8 minutes because gravity propagates at light speed) is solid, supported by the 2017 neutron-star merger observation.

## Black clothes on a sunny day

"Not necessarily hotter" is only true for loose garments with airflow (the 1980 Bedouin robe study). The scenario specifies a loose robe on a breezy day, and the caveat notes tight black clothing in still air is different.

## Perfume crossing a room

Commonly taught as diffusion. Over metres, diffusion alone would take weeks; air currents do almost all the work. The question tests exactly that misconception.

## Wording choices that keep answers true

- Plants in darkness: oxygen release stops almost at once, because water-splitting needs light; respiration continues. The popular claim that some houseplants release oxygen at night was left out because the evidence is weak.
- GPS and relativity: the often-quoted "10 km of error per day" is a simplification (the receiver's clock solution absorbs common drift). The shipped question asks whether satellite clocks run fast or slow, which is unambiguous, and mentions the 11 km only as how far light travels in 38 µs.
- Bacteria vs human cells: uses the 2016 recount (about 1:1), not the old 10:1 figure.
- Mpemba effect ("hot water freezes faster") was deliberately not used: the evidence is contested.

All 149 questions are marked `reviewed` and carry a note that they were AI-authored and self-checked. Numeric claims were recomputed (coin streaks, birthday problem, Bayes, tidal ratio, fold thickness and others). An independent expert pass is still recommended before marking anything `verified`.
