all:
	./build.pl allfast

clean:
	./build.pl clean

checkdeps:
	./build.pl --eachclean && echo "SUCCESS"
