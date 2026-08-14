package dev.portless.store.inventory;

import java.util.List;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;

@RestController
@RequestMapping("/inventory")
public class InventoryController {
    private final InventoryCatalog catalog;

    public InventoryController(InventoryCatalog catalog) {
        this.catalog = catalog;
    }

    @GetMapping
    public List<InventoryItem> catalog() {
        return catalog.items();
    }

    @GetMapping("/{sku}")
    public ResponseEntity<InventoryAvailability> availability(
            @PathVariable String sku,
            @RequestParam(defaultValue = "1") int quantity) {
        if (quantity < 1) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "quantity must be positive");
        }
        return catalog.availability(sku, quantity)
                .map(ResponseEntity::ok)
                .orElseGet(() -> ResponseEntity.notFound().build());
    }
}
