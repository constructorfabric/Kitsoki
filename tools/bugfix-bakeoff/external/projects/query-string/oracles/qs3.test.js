// HIDDEN ORACLE — query-string PR #392, fix commit 19c43d4.
// The cleanly-ADDED regression test (test/parse.js), isolated. The fix commit
// also refactors several existing tests to use encodeURIComponent; we score only
// this added block (RED at baseline 4287e77, GREEN after a correct fix).
// match title: "*bracket-separator* with a URL encoded value*"
test('query strings having a brackets+separator array and format option as `bracket-separator` with a URL encoded value', t => {
	const key = 'foo[]';
	const value = 'a,b,c,d,e,f';
	t.deepEqual(queryString.parse(`?${encodeURIComponent(key)}=${encodeURIComponent(value)}`, {
		arrayFormat: 'bracket-separator',
	}), {
		foo: ['a', 'b', 'c', 'd', 'e', 'f'],
	});
});
