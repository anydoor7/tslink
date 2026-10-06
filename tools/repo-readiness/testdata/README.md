# fnmatch oracle corpus

`fnmatch-corpus.json` lists ruleset ref patterns and, for each, the first release
ref that Ruby's `File.fnmatch(pattern, ref, File::FNM_PATHNAME)` matches, or `""`
when it matches none. GitHub documents that ruleset patterns use this function with
this flag. `TestExclusionsTheFnmatchOracleMatchesAreNeverReady` and
`TestFnmatchOracleMismatchesAreNotMatches` in `../main_test.go` read it.

The refs tried, in this order, are `refs/tags/v0.1.0`, `refs/tags/v1.0.0-rc.1`,
`refs/tags/v10.20.30`, `refs/tags/v1.0.0-0` and `refs/tags/v1.0.0-0a`. The patterns
are the readiness test cases, the five full-ref wildcards from the PR #34 re-review,
and, for every character of `refs/tags/v`, that character replaced by and prefixed
with `*`, `?`, `[c]`, `[cx]` and `**`, each followed by `*`.

The file was generated with Ruby 2.6.10 by the script below and checked in
unchanged; rerunning the script reproduces it byte for byte.

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
tags = ['refs/tags/v0.1.0', 'refs/tags/v1.0.0-rc.1', 'refs/tags/v10.20.30', 'refs/tags/v1.0.0-0', 'refs/tags/v1.0.0-0a']
rows = patterns.uniq.map do |pattern|
  witness = tags.find { |tag| File.fnmatch(pattern, tag, File::FNM_PATHNAME) }
  {pattern: pattern, witness: witness || ''}
end
File.write('fnmatch-corpus.json', JSON.pretty_generate(rows) + "\n")
```

It holds 127 patterns, 76 of which match a release ref.

SHA-256: `e0b1a73e68dca086d28bdcd9638e89c95e53c2f8cd2240f5f9143500270263e0`
