package dev.portless.store.inventory;

import java.util.List;

public record InventoryResetResult(String status, List<InventoryItem> items) {
}
