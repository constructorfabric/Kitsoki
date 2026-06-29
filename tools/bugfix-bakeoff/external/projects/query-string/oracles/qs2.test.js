// HIDDEN ORACLE — query-string issue #404, fix PR #406 (commit 3e61882).
// The cleanly-ADDED regression test (test/parse.js), isolated. The same PR also
// un-`.failing`s a sibling string[] test; we score only the added number[] block
// (cleanly armed: RED at baseline 88e1e36, GREEN after a correct fix).
// match title: "*and type: number*"
test('types option: single element with `{arrayFormat: "comma"}, and type: number[]`', t => {
	t.deepEqual(queryString.parse('a=1', {
		arrayFormat: 'comma',
		types: {
			a: 'number[]',
		},
	}), {
		a: [1],
	});
});
