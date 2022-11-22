%module swsscommon

%{
#include "dbconnector.h"
#include "producerstatetable.h"
using namespace swss;
%}

%include "std_string.i"
%include "std_vector.i"
%include "std_pair.i"
%include "typemaps.i"

%template(FieldValuePair) std::pair<std::string, std::string>;
%template(FieldValuePairs) std::vector<std::pair<std::string, std::string>>;

%include "dbconnector.h"
%include "producerstatetable.h"
