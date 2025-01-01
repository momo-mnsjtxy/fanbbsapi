package com.Fanbbs.entity;

import lombok.Data;

import java.io.Serializable;

@Data
public class Swiper implements Serializable {
    private static final long serialVersionUID = 1L;
    private Integer id;
    private String title;
    private String description;

    private Integer type;
    private String url;
    private Integer article_id;

    private String image;
    private Integer created;
}
