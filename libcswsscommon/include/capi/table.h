#ifndef _C_TABLE_H
#define _C_TABLE_H

#include <hiredis/hiredis.h>
#include <stdbool.h>

#include "producertable.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef void *db_connector_t2;
typedef void *redis_pipeline_t;
typedef void *table_t;

// ProducerStateTable::ProducerStateTable(DBConnector *db, std::string tableName)
table_t table_new(db_connector_t2 db, const char *tableName);
// ProducerStateTable::ProducerStateTable(RedisPipeline *pipeline, std::string tableName, bool buffered = false)
table_t table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered);

// ProducerStateTable::~ProducerStateTable()
void table_delete(table_t pt);

// void ProducerStateTable::setBuffered(bool buffered)
void table_set_buffered(table_t pt, bool buffered);

// void ProducerStateTable::set(std::string key,
//                         std::vector<FieldValueTuple> &values,
//                         std::string op = SET_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void table_set(table_t pt,
                        const char *key,
                        const field_value_tuple_t *values,
                        size_t count,
                        const char *op,
                        const char *prefix);

// void ProducerStateTable::del(std::string key,
//                         std::string op = DEL_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void table_del(table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix);

// void ProducerStateTable::flush()
void table_flush(table_t pt);

#ifdef __cplusplus
}
#endif

#endif
