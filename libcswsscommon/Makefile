RM=rm -f

INCLUDES= \
	-Iinclude \
	-I$(SWSSCOMMON_SRC)/common \
	-I/usr/include/swss

CPPFLAGS=-g -std=c++11 $(INCLUDES)
LDFLAGS=-g -shared

SRCS=src/dbconnector.cpp src/producertable.cpp src/producerstatetable.cpp src/table.cpp
OBJS=$(SRCS:.cpp=.o)
%.o: %.cpp
	$(CXX) $(CPPFLAGS) -c $< -o $@

.PHONY: all swsscommon clean install

all: swsscommon

swsscommon: $(OBJS)
	$(AR) rvs libcswsscommon.a $(OBJS)

install: swsscommon
	cp libcswsscommon.a /usr/lib # FIXME: better use install
	cp -r include/* /usr/include

clean:
	$(RM) $(OBJS)
	$(RM) libcswsscommon.a
