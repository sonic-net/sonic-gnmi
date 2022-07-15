#include <capi/producerstatetable.h>
#include <producerstatetable.h>
#include <dbconnector.h>
#include <redispipeline.h>

#include <string>
#include <vector>
#include <tuple>

producer_state_table_t producer_state_table_new(db_connector_t db, const char *tableName)
{
    auto pt = new swss::ProducerStateTable(static_cast<swss::DBConnector*>(db), std::string(tableName));
    return static_cast<producer_state_table_t>(pt);
}

producer_state_table_t producer_state_table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered)
{
    auto pt = new swss::ProducerStateTable(static_cast<swss::RedisPipeline*>(pipeline), std::string(tableName), buffered);
    return static_cast<producer_state_table_t>(pt);
}

void producer_state_table_delete(producer_state_table_t pt)
{
    delete static_cast<swss::ProducerStateTable*>(pt);
}

void producer_state_table_set_buffered(producer_state_table_t pt, bool buffered)
{
    static_cast<swss::ProducerStateTable*>(pt)->setBuffered(buffered);
}

void producer_state_table_set(producer_state_table_t pt,
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
    static_cast<swss::ProducerStateTable*>(pt)->set(std::string(key), tuples, std::string(op), std::string(prefix));
}

void producer_state_table_del(producer_state_table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix)
{
    static_cast<swss::ProducerStateTable*>(pt)->del(std::string(key), std::string(op), std::string(prefix));
}

void producer_state_table_flush(producer_state_table_t pt)
{
    static_cast<swss::ProducerStateTable*>(pt)->flush();
}
