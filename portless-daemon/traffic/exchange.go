package traffic

import "github.com/runportless/portless/portless-daemon/model"

func exchangeSummary(exchange model.TrafficExchange) model.TrafficExchange {
	exchange.RequestHeaders = nil
	exchange.ResponseHeaders = nil
	exchange.RequestBody = ""
	exchange.ResponseBody = ""
	exchange.RequestBodyTruncated = false
	exchange.ResponseBodyTruncated = false
	if exchange.TCP != nil {
		tcp := *exchange.TCP
		tcp.RequestMessages = nil
		tcp.ResponseMessages = nil
		exchange.TCP = &tcp
	}
	return exchange
}

func cloneExchange(exchange model.TrafficExchange) model.TrafficExchange {
	exchange.RequestHeaders = cloneHeaders(exchange.RequestHeaders)
	exchange.ResponseHeaders = cloneHeaders(exchange.ResponseHeaders)
	if exchange.TCP != nil {
		tcp := *exchange.TCP
		tcp.RequestMessages = cloneMessages(tcp.RequestMessages)
		tcp.ResponseMessages = cloneMessages(tcp.ResponseMessages)
		exchange.TCP = &tcp
	}
	return exchange
}

func cloneMessages(messages []model.TrafficMessage) []model.TrafficMessage {
	if messages == nil {
		return nil
	}
	result := make([]model.TrafficMessage, len(messages))
	for index, message := range messages {
		result[index] = message
		result[index].Fields = append([]model.TrafficMessageField(nil), message.Fields...)
	}
	return result
}

func exchangePayloadBytes(exchange model.TrafficExchange) int64 {
	total := int64(len(exchange.RequestBody) + len(exchange.ResponseBody))
	for _, headers := range []map[string][]string{exchange.RequestHeaders, exchange.ResponseHeaders} {
		for name, values := range headers {
			total += int64(len(name))
			for _, value := range values {
				total += int64(len(value))
			}
		}
	}
	if exchange.TCP == nil {
		return total
	}
	for _, messages := range [][]model.TrafficMessage{exchange.TCP.RequestMessages, exchange.TCP.ResponseMessages} {
		for _, message := range messages {
			total += int64(len(message.Content) + len(message.Summary) + len(message.Type) + len(message.ContentType))
			for _, field := range message.Fields {
				total += int64(len(field.Name) + len(field.Value))
			}
		}
	}
	return total
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		result[name] = append([]string(nil), values...)
	}
	return result
}
