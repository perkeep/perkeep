all:
	go install --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`git log --pretty=format:'%ad-%h' --abbrev-commit --date=short -1` `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` ./pkg/... ./server/... ./cmd/... ./third_party/...

# Workaround Go bug where the $GOPATH/pkg cache doesn't know about tag changes.
# Useful when you accidentally run "make" and then "make presubmit" doesn't work.
# See https://code.google.com/p/go/issues/detail?id=4443
forcefull:
	go install -a --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server

checkjs:
	perl -e 'require "misc/get_closure.pl"; get_closure_lib(); get_closure_compiler();'
	if [ -f server/camlistored/newui/all.js ]; then rm server/camlistored/newui/all.js; fi
	# This will generate non working code for now, since camli.js, SHA1.js, Crypto.js,
	# and base64.js are not explicitely declared as dependencies.
	tmp/closure-lib/closure/bin/build/closurebuilder.py\
		--root tmp/closure-lib/ \
		--root server/camlistored/ui/ \
		--root server/camlistored/newui/ \
		--namespace="camlistore.IndexPage" \
		--output_mode=compiled \
		--compiler_jar=tmp/closure-compiler/compiler.jar \
		--compiler_flags="--compilation_level=ADVANCED_OPTIMIZATIONS" \
	> server/camlistored/newui/all.js
