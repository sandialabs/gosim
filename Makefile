include $(GOROOT)/src/Make.inc

TARG=readOSM
GOFILES=\
	readOSM.go\

# fix this later
all:
	$(GC) $(GCIMPORTS) -o osm.$O osm.go
	gopack crg osm.a osm.$O
	$(GC) $(GCIMPORTS) -o readOSM.$O readOSM.go
	$(LD) $(LDIMPORTS) -o readOSM readOSM.$O

#include $(GOROOT)/src/Make.cmd

#smoketest: $(TARG)
#	(cd testdata; ./test.sh)
