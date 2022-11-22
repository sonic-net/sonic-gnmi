RM=rm -f

SWIG_FLAG = -c++ -go -intgosize 64
ifeq ($(CONFIGURED_ARCH),arm64)
SWIG_FLAG += -DSWIGWORDSIZE64
endif

.PHONY: all clean

all:
	swig $(SWIG_FLAG) -I/usr/include/swss/ swsscommon.i

clean:
	$(RM) *.go *.cxx