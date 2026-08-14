package dev.portless.store.inventory;

import java.util.Comparator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;

import org.springframework.stereotype.Service;

@Service
public class InventoryCatalog {
    private final Map<String, InventoryItem> items = Map.of(
            "coffee-mug", new InventoryItem("coffee-mug", "Ceramic Coffee Mug", 24, "ord-01"),
            "mechanical-keyboard", new InventoryItem("mechanical-keyboard", "Mechanical Keyboard", 8, "ord-01"),
            "usb-c-cable", new InventoryItem("usb-c-cable", "USB-C Cable", 0, "dfw-02"));

    public List<InventoryItem> items() {
        return items.values().stream()
                .sorted(Comparator.comparing(InventoryItem::sku))
                .toList();
    }

    public Optional<InventoryAvailability> availability(String sku, int requested) {
        InventoryItem item = items.get(sku.toLowerCase(Locale.ROOT));
        if (item == null) {
            return Optional.empty();
        }
        return Optional.of(new InventoryAvailability(
                item.sku(),
                item.name(),
                requested,
                item.onHand(),
                item.onHand() >= requested,
                item.warehouse()));
    }
}
