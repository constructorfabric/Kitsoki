// HIDDEN ORACLE — query-string issue #336, fix ec67fea.
// The regression test the real fix added (test/parse.js), isolated so the
// scorer can overlay it on any candidate's tree without leaking the rest of the
// fix's diff. Appended to a copy of test/parse.js, so `test`/`queryString` are
// already in scope. RED at baseline 2e1f45a, GREEN after a correct fix.
// match title: "single value with encoded separator should not be split into array"
test('single value with encoded separator should not be split into array', t => {
	// Test for issue #336 - encoded separators should not cause array splitting
	const value = encodeURIComponent('a|b'); // 'a%7Cb'
	t.deepEqual(queryString.parse(`foo=${value}`, {
		arrayFormat: 'separator',
		arrayFormatSeparator: '|',
	}), {foo: 'a|b'});

	// Multiple values with encoded separators in them
	const value1 = encodeURIComponent('a|b');
	const value2 = encodeURIComponent('c|d');
	t.deepEqual(queryString.parse(`foo=${value1}|${value2}`, {
		arrayFormat: 'separator',
		arrayFormatSeparator: '|',
	}), {foo: ['a|b', 'c|d']});
});
