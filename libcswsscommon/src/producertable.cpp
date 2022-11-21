#include <capi/producertable.h>
#include <producertable.h>
#include <dbconnector.h>
#include <redispipeline.h>

#include <string>
#include <vector>
#include <tuple>

producer_table_t producer_table_new(db_connector_t db, const char *tableName)
{
    auto pt = new swss::ProducerTable(static_cast<swss::DBConnector*>(db), std::string(tableName));
    return static_cast<producer_table_t>(pt);
}

producer_table_t producer_table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered)
{
    auto pt = new swss::ProducerTable(static_cast<swss::RedisPipeline*>(pipeline), std::string(tableName), buffered);
    return static_cast<producer_table_t>(pt);
}

producer_table_t producer_table_new3(db_connector_t db, const char *tableName, const char *dumpFile)
{
    auto pt = new swss::ProducerTable(static_cast<swss::DBConnector*>(db), std::string(tableName), std::string(dumpFile));
    return static_cast<producer_table_t>(pt);
}

void producer_table_delete(producer_table_t pt)
{
    delete static_cast<swss::ProducerTable*>(pt);
}

void producer_table_set_buffered(producer_table_t pt, bool buffered)
{
    static_cast<swss::ProducerTable*>(pt)->setBuffered(buffered);
}

void producer_table_set(producer_table_t pt,
                        const char *key,
                        const field_value_tuple_t *values,
                        size_t count,
                        const char *op,
                        const char *prefix)
{
    std::vector<swss::FieldValueTuple> tuples;
    for(size_t i = 0; i < count; i++)
    {
        auto tuple = std::make_pair(std::string(values[i].field), std::string(values[i].value));
        tuples.push_back(tuple);
    }
    static_cast<swss::ProducerTable*>(pt)->set(std::string(key), tuples, std::string(op), std::string(prefix));
}

void producer_table_del(producer_table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix)
{
    static_cast<swss::ProducerTable*>(pt)->del(std::string(key), std::string(op), std::string(prefix));
}

void producer_table_flush(producer_table_t pt)
{
    static_cast<swss::ProducerTable*>(pt)->flush();
}
