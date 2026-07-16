# 0xbin word lists

These version 1 lists contain 128 adjectives and 128 nouns. Selecting two
adjectives and one noun produces exactly 2,097,152 combinations
(`128 * 128 * 128`), close to the approximately two million combinations
accepted by the product specification.

The vocabulary was independently curated for 0xbin from common English words.
LocalSend's human-friendly adjective/noun device aliases inspired the approach,
but no LocalSend word list was copied. LocalSend is licensed under Apache-2.0:
<https://github.com/localsend/localsend>.

The lists avoid names, profanity, sensitive labels, separators, digits, and
non-ASCII characters. They are distributed under 0xbin's MIT license; see
[`../LICENSE`](../LICENSE).

The Go validation tests enforce the list sizes, syntax, duplicate-source-word
rules, and uniqueness of every concatenated slug. Changing either file requires
reviewing the vocabulary and updating those tests deliberately.
