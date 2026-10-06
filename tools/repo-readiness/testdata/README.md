# fnmatch oracle corpus

`fnmatch-corpus.json` lists ruleset ref patterns and, for each, the first release
ref that Ruby's `File.fnmatch(pattern, ref, File::FNM_PATHNAME)` matches, or `""`
when it matches none, and under `fnmatch` its result against each of the three
witnesses in `releaseTagWitnesses`. GitHub documents that ruleset patterns use this
function with this flag. `TestTrustedExclusionsMatchTheWitnessesAsFnmatchDoes`,
`TestExclusionsTheFnmatchOracleMatchesAreNeverReady`,
`TestFnmatchOracleRejectionsAreNotMatches` and
`TestExclusionsOutsideTheWhitelistAreUnknown` in `../main_test.go` read it.

The refs tried for `witness`, in this order, are `refs/tags/v0.1.0`,
`refs/tags/v1.0.0-rc.1`, `refs/tags/v10.20.30`, `refs/tags/v1.0.0-0` and
`refs/tags/v1.0.0-0a`; the witnesses under `fnmatch` are `refs/tags/v0.1.0`,
`refs/tags/v1.2.3-rc.1` and `refs/tags/v10.20.30`. The patterns are the readiness
test cases, the five full-ref wildcards from the PR #34 re-review, and, for every
character of `refs/tags/v`, that character replaced by and prefixed with `*`, `?`,
`[c]`, `[cx]` and `**`, each followed by `*`. Then come seven bracket ranges that
Go's `path.Match` may read differently from `File.fnmatch`: descending ones, which
`File.fnmatch` reads as their two endpoints and `path.Match` as empty, and ones with
escaped or non-ASCII endpoints. The next nine are outside the grammar
`witnessTrusted` accepts: a `[!` inside a class, a `[`, `]` or `-` as a class
member, an escape, a range from a digit to a letter, and `[!]`. The last sixteen are
inside it: negated classes, ranges of each kind, and patterns that match only some
of the witnesses.

The file was generated with Ruby 2.6.10 (`ruby 2.6.10p210`, macOS `/usr/bin/ruby`)
by saving the script below as `gen.rb` in this directory and running `ruby gen.rb`,
and checked in unchanged; rerunning the script reproduces it byte for byte.

```ruby
require 'json'

prefix = 'refs/tags/v'
patterns = [
  'refs/tags/v*', 'refs/tags/v0.1.0', 'refs/tags/v*-rc*', '~ALL',
  'refs/tags/*', 'refs/tags/?*', 'refs/*/v*', 'refs/tags/[uvw]*',
  'refs/tags/nightly-*', 'refs/heads/v*', 'v*', '~DEFAULT_BRANCH',
  '~NEW_TOKEN', 'ref*/tags/v*', 'refs*/tags/v*', 'ref?/tags/v*',
  'r?fs/tags/v*', 'ref[s]/tags/v*', '*/tags/v*', '**/v*',
]
prefix.each_char.with_index do |letter, i|
  ['*', '?', "[#{letter}]", "[#{letter}x]", '**'].each do |token|
    patterns << prefix[0...i] + token + prefix[i+1..-1] + '*'
    patterns << prefix[0...i] + token + prefix[i..-1] + '*'
  end
end
patterns += ['refs/tags/[' + 'v' + ']*', 'refs/[t]ags/v*', 'refs/[x]ags/v*', 'refs/tags/v/**', 'refs/tags/v[0-9]*']
patterns += ['refs/[!t-a]ags/v*', 'refs/[^t-a]ags/v*', 'refs/tags/[!v-a]*', 'refs/[t-a]ags/v*',
             'refs/[!z-a]ags/v*', 'refs/[\\s-\\u]ags/v*', "refs/[!\uff41-\uff5a]ags/v*"]
patterns += ['refs/tags/v[![!-9]*', 'refs/tags/v[![!-z]*', 'refs/tags/v[[]*', 'refs/tags/v[]]*',
             'refs/tags/v[a-]*', 'refs/tags/v[-a]*', 'refs/tags/v[0-9]\\*', 'refs/tags/v[0-z]*', 'refs/tags/v[!]*']
patterns += ['refs/tags/[!n]*', 'refs/tags/[^n]*', 'refs/tags/v[!0-9]*', 'refs/tags/v[^1-9]*', 'refs/tags/v[!1]*',
             'refs/tags/v[0-0]*', 'refs/tags/v1[0-9]*', 'refs/tags/v1[._]*', 'refs/tags/v[0-9][!.]*',
             'refs/tags/v[A-Z]*', 'refs/tags/[!A-Z]*', 'refs/tags/v[0-9a-zA-Z]*', 'refs/tags/v?.?.?',
             'refs/tags/v*-[a-z][a-z].?', 'refs/**/v[0-9]*', '**/[!.]*/v[0-9a-z._]*']
tags = ['refs/tags/v0.1.0', 'refs/tags/v1.0.0-rc.1', 'refs/tags/v10.20.30', 'refs/tags/v1.0.0-0', 'refs/tags/v1.0.0-0a']
witnesses = ['refs/tags/v0.1.0', 'refs/tags/v1.2.3-rc.1', 'refs/tags/v10.20.30']
rows = patterns.uniq.map do |pattern|
  witness = tags.find { |tag| File.fnmatch(pattern, tag, File::FNM_PATHNAME) }
  fnmatch = witnesses.map { |ref| [ref, File.fnmatch(pattern, ref, File::FNM_PATHNAME)] }.to_h
  {pattern: pattern, witness: witness || '', fnmatch: fnmatch}
end
File.write('fnmatch-corpus.json', JSON.pretty_generate(rows) + "\n")
```

It holds 159 patterns, 96 of which match a release ref.

SHA-256: `012da3f9ab7c216c90583f5194bd9809f8ed6487c25232937a70fb969eb01ccf`
