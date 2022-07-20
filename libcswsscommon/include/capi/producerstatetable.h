#ifndef _C_PRODUCERSTATETABLE_H
#define _C_PRODUCERSTATETABLE_H

#include <hiredis/hiredis.h>
#include <stdbool.h>

#include "producertable.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef void *db_connector_t2;
typedef void *redis_pipeline_t;
typedef void *producer_state_table_t;

// ProducerStateTable::ProducerStateTable(DBConnector *db, std::string tableName)
producer_state_table_t producer_state_table_new(db_connector_t2 db, const char *tableName);
// ProducerStateTable::ProducerStateTable(RedisPipeline *pipeline, std::string tableName, bool buffered = false)
producer_state_table_t producer_state_table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered);

// ProducerStateTable::~ProducerStateTable()
void producer_state_table_delete(producer_state_table_t pt);

// void ProducerStateTable::setBuffered(bool buffered)
void producer_state_table_set_buffered(producer_state_table_t pt, bool buffered);

// void ProducerStateTable::set(std::string key,
//                         std::vector<FieldValueTuple> &values,
//                         std::string op = SET_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void producer_state_table_set(producer_state_table_t pt,
                        const char *key,
                        const field_value_tuple_t *values,
                        size_t count,
                        const char *op,
                        const char *prefix);

// void ProducerStateTable::del(std::string key,
//                         std::string op = DEL_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void producer_state_table_del(producer_state_table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix);

// void ProducerStateTable::flush()
void producer_state_table_flush(producer_state_table_t pt);

#ifdef __cplusplus
}
#endif

#endif
