// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const paginationPages = (currentPage: number, totalPages: number): (number | "ellipsis")[] => {
    const pages: (number | "ellipsis")[] = [];
    if (totalPages <= 5) {
        for (let i = 1; i <= totalPages; i++) pages.push(i);
        return pages;
    }
    if (currentPage <= 3) {
        for (let i = 1; i <= 4; i++) pages.push(i);
        pages.push("ellipsis", totalPages);
    } else if (currentPage >= totalPages - 2) {
        pages.push(1, "ellipsis");
        for (let i = totalPages - 3; i <= totalPages; i++) pages.push(i);
    } else {
        pages.push(1, "ellipsis");
        for (let i = currentPage - 1; i <= currentPage + 1; i++) pages.push(i);
        pages.push("ellipsis", totalPages);
    }
    return pages;
};
