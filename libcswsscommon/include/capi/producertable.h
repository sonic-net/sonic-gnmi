#ifndef _C_PRODUCERTABLE_H
#define _C_PRODUCERTABLE_H

#include <hiredis/hiredis.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef void *db_connector_t;
typedef void *redis_pipeline_t;
typedef void *producer_table_t;

typedef struct field_value_tuple
{
    const char *field;
    const char *value;
} field_value_tuple_t;

// ProducerTable::ProducerTable(DBConnector *db, std::string tableName)
producer_table_t producer_table_new(db_connector_t db, const char *tableName);
// ProducerTable::ProducerTable(RedisPipeline *pipeline, std::string tableName, bool buffered = false)
producer_table_t producer_table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered);
// ProducerTable::ProducerTable(DBConnector *db, std::string tableName, std::string dumpFile)
producer_table_t producer_table_new3(db_connector_t db, const char *tableName, const char *dumpFile);

// ProducerTable::~ProducerTable()
void producer_table_delete(producer_table_t pt);

// void ProducerTable::setBuffered(bool buffered)
void producer_table_set_buffered(producer_table_t pt, bool buffered);

// void ProducerTable::set(std::string key,
//                         std::vector<FieldValueTuple> &values,
//                         std::string op = SET_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void producer_table_set(producer_table_t pt,
                        const char *key,
                        const field_value_tuple_t *values,
                        size_t count,
                        const char *op,
                        const char *prefix);

// void ProducerTable::del(std::string key,
//                         std::string op = DEL_COMMAND,
//                         std::string prefix = EMPTY_PREFIX)
void producer_table_del(producer_table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix);

// void ProducerTable::flush()
void producer_table_flush(producer_table_t pt);

#ifdef __cplusplus
}
#endif

#endif
